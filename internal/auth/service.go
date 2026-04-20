package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/macabc/muzika/internal/bus"
	"github.com/macabc/muzika/internal/db"
)

// bcryptCost matches the old Spring service. ~100 ms per hash on a Pi 3 —
// acceptable for register/login handlers. Don't raise this on Pi 3 hardware.
const bcryptCost = 10

// Service-layer errors. Handlers translate these to HTTP status codes.
var (
	ErrBadCredentials = errors.New("auth: bad credentials")
	ErrForbidden      = errors.New("auth: forbidden")
)

// dummyHash is a pre-computed valid bcrypt hash used to equalize timing
// between "unknown user" and "wrong password" login failures. Computed
// once on first use to avoid a startup penalty when auth is never called.
var (
	dummyHashOnce sync.Once
	dummyHashBuf  []byte
)

func dummyHash() []byte {
	dummyHashOnce.Do(func() {
		h, err := bcrypt.GenerateFromPassword(
			[]byte("muzika-timing-dummy-password"), bcryptCost)
		if err != nil {
			panic(fmt.Errorf("auth: init dummy bcrypt hash: %w", err))
		}
		dummyHashBuf = h
	})
	return dummyHashBuf
}

// Service holds the auth module's dependencies.
type Service struct {
	db         *sql.DB
	repo       *Repo
	jwt        *JWT
	bus        *bus.Bus
	dispatcher *bus.OutboxDispatcher
}

// NewService wires the pieces together.
func NewService(
	sqlDB *sql.DB,
	secret string,
	expiration time.Duration,
	b *bus.Bus,
	d *bus.OutboxDispatcher,
) *Service {
	repo := NewRepo(sqlDB)
	s := &Service{
		db:         sqlDB,
		repo:       repo,
		bus:        b,
		dispatcher: d,
	}
	s.jwt = NewJWT(secret, expiration, repo.GetTokenVersion)
	return s
}

// Verifier exposes the JWT as an httpx.JWTVerifier.
func (s *Service) Verifier() *JWT { return s.jwt }

// Register validates the request, bcrypts the password, inserts an auth_users
// row and a UserCreated outbox row in a single transaction, and wakes the
// outbox dispatcher.
func (s *Service) Register(ctx context.Context, req RegisterRequest) (User, error) {
	if err := validateUsername(req.Username); err != nil {
		return User{}, err
	}
	if err := validatePassword(req.Password); err != nil {
		return User{}, err
	}
	if err := validateEmail(req.Email); err != nil {
		return User{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		return User{}, fmt.Errorf("bcrypt: %w", err)
	}

	// Stamp CreatedAt from the Go clock before the insert and pass it in
	// explicitly — that way the returned User already has the authoritative
	// value without a post-commit GetByID round-trip. The DB column still
	// has a DEFAULT (unixepoch()) as a safety net for other callers.
	u := User{
		ID:           uuid.New(),
		Username:     req.Username,
		PasswordHash: string(hash),
		Email:        req.Email,
		TokenVersion: 0,
		CreatedAt:    time.Now().UTC(),
	}

	err = db.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		if err := s.repo.Create(ctx, tx, u); err != nil {
			return err
		}
		return bus.InsertOutboxTx(ctx, tx, bus.TypeUserCreated, bus.UserCreated{
			UserID:   u.ID,
			Username: u.Username,
		})
	})
	if err != nil {
		return User{}, err
	}

	if s.dispatcher != nil {
		s.dispatcher.Wake()
	}
	return u, nil
}

// Login verifies credentials and issues a JWT. Unknown-user and wrong-password
// both return ErrBadCredentials; timing is equalized by running bcrypt against
// a dummy hash in the unknown-user path.
//
// Timing-attack caveat: the equalization here is bcrypt-layer only. The DB
// lookup itself (GetByUsername) is NOT constant-time — an index hit and an
// index miss have measurably different latencies under load, and so does a
// large vs. small auth_users table. For muzika's design point (single-user
// or small-family LAN deployment) this is acceptable; side-channel enumeration
// over the LAN is not in the threat model. If this service is ever exposed
// to the public internet, revisit: either pre-issue the DB query in the
// unknown path, or front the endpoint with per-IP rate limiting.
func (s *Service) Login(ctx context.Context, req LoginRequest) (LoginResponse, error) {
	if req.Username == "" || req.Password == "" {
		// Still burn a bcrypt round so we don't leak the "empty-field" case.
		_ = bcrypt.CompareHashAndPassword(dummyHash(), []byte(req.Password))
		return LoginResponse{}, ErrBadCredentials
	}
	u, err := s.repo.GetByUsername(ctx, req.Username)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			_ = bcrypt.CompareHashAndPassword(dummyHash(), []byte(req.Password))
			return LoginResponse{}, ErrBadCredentials
		}
		return LoginResponse{}, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)); err != nil {
		return LoginResponse{}, ErrBadCredentials
	}
	token, err := s.jwt.Issue(u.ID, u.TokenVersion)
	if err != nil {
		return LoginResponse{}, err
	}
	return LoginResponse{
		Token:    token,
		UserID:   u.ID,
		Username: u.Username,
		Email:    u.Email,
	}, nil
}

// LogoutAll bumps the user's token_version so every outstanding JWT fails Verify.
func (s *Service) LogoutAll(ctx context.Context, userID uuid.UUID) error {
	return s.repo.IncrementTokenVersion(ctx, userID)
}

// Delete removes a user. FK cascade cleans up playlists, queue entries, user_songs.
// Emits UserDeleted via the outbox in the same transaction.
// The caller (handler) authorizes that userID is the authenticated caller.
func (s *Service) Delete(ctx context.Context, userID uuid.UUID) error {
	err := db.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		if err := bus.InsertOutboxTx(ctx, tx, bus.TypeUserDeleted, bus.UserDeleted{
			UserID: userID,
		}); err != nil {
			return err
		}
		return s.repo.Delete(ctx, tx, userID)
	})
	if err != nil {
		return err
	}
	if s.dispatcher != nil {
		s.dispatcher.Wake()
	}
	return nil
}
