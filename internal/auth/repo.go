package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/db"
)

// Repo-layer errors.
var (
	ErrNotFound  = errors.New("auth: user not found")
	ErrDuplicate = errors.New("auth: username or email already exists")
)

// Repo is the persistence surface for the auth module. All queries target
// auth_users. User deletion is a single DELETE; FK cascades handle dependents.
type Repo struct {
	db *sql.DB
}

// NewRepo constructs a Repo around an open *sql.DB.
func NewRepo(db *sql.DB) *Repo { return &Repo{db: db} }

// Create inserts a new user row within the caller-owned transaction.
// Pairs with a bus.InsertOutboxTx call in the same tx.
//
// created_at: if u.CreatedAt is non-zero, it's written verbatim so the
// service layer's clock reading is authoritative. If zero, we fall back to
// the column's DEFAULT (unixepoch()) — which keeps direct-repo callers
// (tests, tools) honest without forcing them to pre-populate the field.
func (r *Repo) Create(ctx context.Context, tx *sql.Tx, u User) error {
	var err error
	if u.CreatedAt.IsZero() {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO auth_users (id, username, password, email, token_version)
			 VALUES (?, ?, ?, ?, ?)`,
			u.ID.String(), u.Username, u.PasswordHash, nullString(u.Email), u.TokenVersion,
		)
	} else {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO auth_users (id, username, password, email, token_version, created_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			u.ID.String(), u.Username, u.PasswordHash, nullString(u.Email), u.TokenVersion, u.CreatedAt.Unix(),
		)
	}
	if err != nil {
		if db.IsUniqueErr(err) {
			return ErrDuplicate
		}
		return fmt.Errorf("insert user: %w", err)
	}
	return nil
}

// GetByID returns a user by primary key.
func (r *Repo) GetByID(ctx context.Context, id uuid.UUID) (User, error) {
	return r.scanOne(ctx,
		`SELECT id, username, password, email, token_version, created_at, updated_at
		 FROM auth_users WHERE id = ?`,
		id.String())
}

// GetByUsername is used by login.
func (r *Repo) GetByUsername(ctx context.Context, username string) (User, error) {
	return r.scanOne(ctx,
		`SELECT id, username, password, email, token_version, created_at, updated_at
		 FROM auth_users WHERE username = ?`,
		username)
}

// GetTokenVersion loads just the tv column for Verify's fast path.
func (r *Repo) GetTokenVersion(ctx context.Context, id uuid.UUID) (int, error) {
	var tv int
	err := r.db.QueryRowContext(ctx,
		`SELECT token_version FROM auth_users WHERE id = ?`,
		id.String()).Scan(&tv)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("select token_version: %w", err)
	}
	return tv, nil
}

// IncrementTokenVersion backs /api/auth/logout-all. Also updates updated_at.
func (r *Repo) IncrementTokenVersion(ctx context.Context, id uuid.UUID) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE auth_users
		 SET    token_version = token_version + 1,
		        updated_at    = unixepoch()
		 WHERE  id = ?`,
		id.String())
	if err != nil {
		return fmt.Errorf("increment token_version: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a user row within the caller-owned transaction.
// Callers pair this with a bus.InsertOutboxTx for UserDeleted.
func (r *Repo) Delete(ctx context.Context, tx *sql.Tx, id uuid.UUID) error {
	res, err := tx.ExecContext(ctx,
		`DELETE FROM auth_users WHERE id = ?`,
		id.String())
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repo) scanOne(ctx context.Context, q string, args ...any) (User, error) {
	var (
		idStr     string
		username  string
		pwHash    string
		email     sql.NullString
		tv        int
		createdAt int64
		updatedAt sql.NullInt64
	)
	err := r.db.QueryRowContext(ctx, q, args...).Scan(
		&idStr, &username, &pwHash, &email, &tv, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("scan user: %w", err)
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return User{}, fmt.Errorf("parse uuid: %w", err)
	}
	u := User{
		ID:           id,
		Username:     username,
		PasswordHash: pwHash,
		TokenVersion: tv,
		CreatedAt:    time.Unix(createdAt, 0).UTC(),
	}
	if email.Valid {
		u.Email = email.String
	}
	if updatedAt.Valid {
		u.UpdatedAt = time.Unix(updatedAt.Int64, 0).UTC()
	}
	return u, nil
}

// nullString returns nil for "" so we write NULL instead of empty-string.
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

