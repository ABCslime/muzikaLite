package auth_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/auth"
	"github.com/macabc/muzika/internal/bus"
	"github.com/macabc/muzika/internal/db"
	"github.com/macabc/muzika/internal/httpx"
)

const (
	testSecret     = "test-secret-do-not-use-in-prod-aaaaaaaaaa"
	testExpiration = 24 * time.Hour
)

// setupTestDB opens a fresh SQLite DB in t.TempDir() with the production
// pragmas (WAL does NOT work on :memory:, so we use a temp file), runs
// the embedded migrations, and registers a cleanup.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "muzika-test.db")
	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.MigrateEmbedded(sqlDB); err != nil {
		t.Fatalf("db.MigrateEmbedded: %v", err)
	}
	return sqlDB
}

// newTestService wires an *auth.Service around a fresh temp DB. Dispatcher
// is nil — outbox rows accumulate so tests can read them directly.
func newTestService(t *testing.T) (*auth.Service, *sql.DB) {
	t.Helper()
	sqlDB := setupTestDB(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)
	svc := auth.NewService(sqlDB, testSecret, testExpiration, b, nil)
	return svc, sqlDB
}

func countRows(t *testing.T, sqlDB *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := sqlDB.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func validRegisterReq(username string) auth.RegisterRequest {
	return auth.RegisterRequest{
		Username: username,
		Password: "correct-horse-battery",
		Email:    username + "@example.com",
	}
}

// ----------------------------------------------------------------------------
// Register
// ----------------------------------------------------------------------------

func TestCreateUser_HappyPath(t *testing.T) {
	svc, sqlDB := newTestService(t)
	ctx := context.Background()

	u, err := svc.Register(ctx, validRegisterReq("alice"))
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if u.ID == uuid.Nil {
		t.Fatal("expected non-zero UUID")
	}
	if u.Username != "alice" {
		t.Errorf("got username %q, want %q", u.Username, "alice")
	}
	if u.TokenVersion != 0 {
		t.Errorf("got token_version %d, want 0", u.TokenVersion)
	}
	if u.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero")
	}

	// Row actually in DB.
	if n := countRows(t, sqlDB, `SELECT COUNT(*) FROM auth_users WHERE id = ?`, u.ID.String()); n != 1 {
		t.Errorf("expected 1 auth_users row, got %d", n)
	}

	// Outbox row exists with correct payload.
	var payload []byte
	err = sqlDB.QueryRow(`SELECT payload FROM outbox WHERE event_type = ?`, bus.TypeUserCreated).Scan(&payload)
	if err != nil {
		t.Fatalf("select outbox: %v", err)
	}
	var ev bus.UserCreated
	if err := json.Unmarshal(payload, &ev); err != nil {
		t.Fatalf("unmarshal UserCreated: %v", err)
	}
	if ev.UserID != u.ID {
		t.Errorf("outbox UserID %v != created %v", ev.UserID, u.ID)
	}
	if ev.Username != "alice" {
		t.Errorf("outbox Username %q, want alice", ev.Username)
	}
}

// TestCreateUser_OutboxOrdering verifies that the user-row insert and outbox
// insert live in the same transaction — if the tx rolls back (simulated via
// the callback returning a non-nil error), neither row exists.
func TestCreateUser_OutboxOrdering(t *testing.T) {
	sqlDB := setupTestDB(t)
	ctx := context.Background()

	repo := auth.NewRepo(sqlDB)
	u := auth.User{
		ID:           uuid.New(),
		Username:     "bob",
		PasswordHash: "$2a$10$unusedunusedunusedunusedunusedunusedunusedunusedunusedunused",
	}

	simulatedErr := fmt.Errorf("simulated rollback")
	err := db.WithTx(ctx, sqlDB, func(tx *sql.Tx) error {
		if err := repo.Create(ctx, tx, u); err != nil {
			return err
		}
		if err := bus.InsertOutboxTx(ctx, tx, bus.TypeUserCreated, bus.UserCreated{
			UserID:   u.ID,
			Username: u.Username,
		}); err != nil {
			return err
		}
		return simulatedErr
	})
	if !errors.Is(err, simulatedErr) {
		t.Fatalf("got err %v, want simulatedErr", err)
	}

	if n := countRows(t, sqlDB, `SELECT COUNT(*) FROM auth_users`); n != 0 {
		t.Errorf("expected 0 auth_users after rollback, got %d", n)
	}
	if n := countRows(t, sqlDB, `SELECT COUNT(*) FROM outbox`); n != 0 {
		t.Errorf("expected 0 outbox rows after rollback, got %d", n)
	}
}

func TestRegister_DuplicateUsername(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	if _, err := svc.Register(ctx, validRegisterReq("alice")); err != nil {
		t.Fatalf("first register: %v", err)
	}
	_, err := svc.Register(ctx, validRegisterReq("alice"))
	if !errors.Is(err, auth.ErrDuplicate) {
		t.Errorf("expected ErrDuplicate, got %v", err)
	}
}

func TestRegister_InvalidInputs(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	cases := []struct {
		name string
		req  auth.RegisterRequest
		want error
	}{
		{"short username", auth.RegisterRequest{Username: "ab", Password: "correct-horse-battery"}, auth.ErrInvalidUsername},
		{"bad username chars", auth.RegisterRequest{Username: "alice!", Password: "correct-horse-battery"}, auth.ErrInvalidUsername},
		{"short password", auth.RegisterRequest{Username: "alice", Password: "short"}, auth.ErrInvalidPassword},
		{"bad email", auth.RegisterRequest{Username: "alice", Password: "correct-horse-battery", Email: "not-an-email"}, auth.ErrInvalidEmail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Register(ctx, tc.req)
			if !errors.Is(err, tc.want) {
				t.Errorf("got %v, want %v", err, tc.want)
			}
		})
	}
}

// ----------------------------------------------------------------------------
// Login
// ----------------------------------------------------------------------------

func TestLogin_CorrectPassword(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	if _, err := svc.Register(ctx, validRegisterReq("alice")); err != nil {
		t.Fatalf("register: %v", err)
	}

	resp, err := svc.Login(ctx, auth.LoginRequest{
		Username: "alice",
		Password: "correct-horse-battery",
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if resp.Token == "" {
		t.Fatal("empty token")
	}
	if resp.Username != "alice" {
		t.Errorf("got username %q, want alice", resp.Username)
	}

	// Issued token should verify against the service's own verifier.
	userID, err := svc.Verifier().Verify(ctx, resp.Token)
	if err != nil {
		t.Fatalf("Verify fresh token: %v", err)
	}
	if userID != resp.UserID {
		t.Errorf("Verify returned %v, want %v", userID, resp.UserID)
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	if _, err := svc.Register(ctx, validRegisterReq("alice")); err != nil {
		t.Fatalf("register: %v", err)
	}
	_, err := svc.Login(ctx, auth.LoginRequest{
		Username: "alice",
		Password: "this-is-not-correct",
	})
	if !errors.Is(err, auth.ErrBadCredentials) {
		t.Errorf("got %v, want ErrBadCredentials", err)
	}
}

// TestLogin_UnknownUser asserts the unknown-user path returns the SAME error
// as wrong-password — no username enumeration.
func TestLogin_UnknownUser(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	_, err := svc.Login(ctx, auth.LoginRequest{
		Username: "does-not-exist",
		Password: "whatever",
	})
	if !errors.Is(err, auth.ErrBadCredentials) {
		t.Errorf("got %v, want ErrBadCredentials (same as wrong-password)", err)
	}
}

// ----------------------------------------------------------------------------
// JWT
// ----------------------------------------------------------------------------

func TestJWT_TokenVersionMismatch(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	u, err := svc.Register(ctx, validRegisterReq("alice"))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	resp, err := svc.Login(ctx, auth.LoginRequest{Username: "alice", Password: "correct-horse-battery"})
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	// Token should verify before logout-all.
	if _, err := svc.Verifier().Verify(ctx, resp.Token); err != nil {
		t.Fatalf("pre-logout Verify: %v", err)
	}

	if err := svc.LogoutAll(ctx, u.ID); err != nil {
		t.Fatalf("LogoutAll: %v", err)
	}

	// Old token must now be rejected as revoked.
	_, err = svc.Verifier().Verify(ctx, resp.Token)
	if !errors.Is(err, auth.ErrRevokedToken) {
		t.Errorf("got %v, want ErrRevokedToken", err)
	}

	// A fresh login after logout-all should still work.
	resp2, err := svc.Login(ctx, auth.LoginRequest{Username: "alice", Password: "correct-horse-battery"})
	if err != nil {
		t.Fatalf("post-logout login: %v", err)
	}
	if _, err := svc.Verifier().Verify(ctx, resp2.Token); err != nil {
		t.Errorf("post-logout fresh token Verify: %v", err)
	}
}

func TestJWT_ExpiredToken(t *testing.T) {
	sqlDB := setupTestDB(t)
	ctx := context.Background()
	repo := auth.NewRepo(sqlDB)

	// Create a user directly so we have a tv to read back.
	u := auth.User{
		ID:           uuid.New(),
		Username:     "carol",
		PasswordHash: "$2a$10$unusedunusedunusedunusedunusedunusedunusedunusedunusedunused",
	}
	err := db.WithTx(ctx, sqlDB, func(tx *sql.Tx) error {
		return repo.Create(ctx, tx, u)
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Construct a JWT with negative expiration → exp claim is in the past.
	j := auth.NewJWT(testSecret, -1*time.Minute, repo.GetTokenVersion)
	token, err := j.Issue(u.ID, 0)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	_, err = j.Verify(ctx, token)
	if !errors.Is(err, auth.ErrExpiredToken) {
		t.Errorf("got %v, want ErrExpiredToken", err)
	}
}

func TestJWT_InvalidSignature(t *testing.T) {
	sqlDB := setupTestDB(t)
	ctx := context.Background()
	repo := auth.NewRepo(sqlDB)

	u := auth.User{
		ID:           uuid.New(),
		Username:     "dave",
		PasswordHash: "$2a$10$unusedunusedunusedunusedunusedunusedunusedunusedunusedunused",
	}
	_ = db.WithTx(ctx, sqlDB, func(tx *sql.Tx) error { return repo.Create(ctx, tx, u) })

	issuer := auth.NewJWT(testSecret, testExpiration, repo.GetTokenVersion)
	token, _ := issuer.Issue(u.ID, 0)

	// Verify with a different secret → signature mismatch.
	verifier := auth.NewJWT("a-different-secret-aaaaaaaaaaaaaaaaaaaaa", testExpiration, repo.GetTokenVersion)
	_, err := verifier.Verify(ctx, token)
	if !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("got %v, want ErrInvalidToken", err)
	}
}

// ----------------------------------------------------------------------------
// Delete (authorization)
// ----------------------------------------------------------------------------

// TestDeleteUser_OtherUser: Alice tries to delete Bob's account → 403, not 204.
// Bob's row must still exist afterward.
func TestDeleteUser_OtherUser(t *testing.T) {
	svc, sqlDB := newTestService(t)
	ctx := context.Background()

	alice, err := svc.Register(ctx, validRegisterReq("alice"))
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	bob, err := svc.Register(ctx, validRegisterReq("bob"))
	if err != nil {
		t.Fatalf("register bob: %v", err)
	}

	h := auth.NewHandler(svc)

	req := httptest.NewRequest(http.MethodDelete, "/api/auth/user/"+bob.ID.String(), nil)
	req.SetPathValue("id", bob.ID.String())
	req = req.WithContext(httpx.WithUserID(req.Context(), alice.ID))

	w := httptest.NewRecorder()
	h.Delete(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("got status %d, want %d", w.Code, http.StatusForbidden)
	}

	// Bob's row must still exist.
	if n := countRows(t, sqlDB, `SELECT COUNT(*) FROM auth_users WHERE id = ?`, bob.ID.String()); n != 1 {
		t.Errorf("expected Bob's row to survive, count=%d", n)
	}
}

func TestDeleteUser_Self(t *testing.T) {
	svc, sqlDB := newTestService(t)
	ctx := context.Background()

	alice, err := svc.Register(ctx, validRegisterReq("alice"))
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	h := auth.NewHandler(svc)
	req := httptest.NewRequest(http.MethodDelete, "/api/auth/user/"+alice.ID.String(), nil)
	req.SetPathValue("id", alice.ID.String())
	req = req.WithContext(httpx.WithUserID(req.Context(), alice.ID))

	w := httptest.NewRecorder()
	h.Delete(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("got status %d, want %d (body=%s)", w.Code, http.StatusNoContent, w.Body.String())
	}
	if n := countRows(t, sqlDB, `SELECT COUNT(*) FROM auth_users WHERE id = ?`, alice.ID.String()); n != 0 {
		t.Errorf("expected Alice's row removed, count=%d", n)
	}
	// UserDeleted outbox row should have been written.
	if n := countRows(t, sqlDB, `SELECT COUNT(*) FROM outbox WHERE event_type = ?`, bus.TypeUserDeleted); n != 1 {
		t.Errorf("expected 1 UserDeleted outbox row, got %d", n)
	}
}

// ----------------------------------------------------------------------------
// Handler error translation
// ----------------------------------------------------------------------------

func TestHandler_RegisterDuplicate409(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	if _, err := svc.Register(ctx, validRegisterReq("alice")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	h := auth.NewHandler(svc)

	body, _ := json.Marshal(validRegisterReq("alice"))
	req := httptest.NewRequest(http.MethodPost, "/api/auth/user", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.Register(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("got %d, want 409 (body=%s)", w.Code, w.Body.String())
	}
}

func TestHandler_LoginBadCredentials401(t *testing.T) {
	svc, _ := newTestService(t)
	h := auth.NewHandler(svc)

	body, _ := json.Marshal(auth.LoginRequest{Username: "nobody", Password: "wrongpassword"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.Login(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", w.Code)
	}
	if !strings.Contains(w.Body.String(), "invalid credentials") {
		t.Errorf("body should mention 'invalid credentials', got %s", w.Body.String())
	}
}

// Ensure the io.Discard import is used even if slog-based setup changes later.
var _ = io.Discard
