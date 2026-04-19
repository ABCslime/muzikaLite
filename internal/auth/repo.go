package auth

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a lookup yields no row.
var ErrNotFound = errors.New("auth: user not found")

// ErrDuplicate is returned on a uniqueness violation (username or email).
var ErrDuplicate = errors.New("auth: duplicate")

// Repo is the persistence surface for the auth module. All queries target
// auth_users. User deletion is a single DELETE; FK cascade handles the rest
// (playlists, playlist_songs, queue_entries, queue_user_songs).
type Repo struct {
	db *sql.DB
}

// NewRepo constructs a Repo around an open *sql.DB.
func NewRepo(db *sql.DB) *Repo { return &Repo{db: db} }

// Create inserts a new user. TODO(port): Phase 4.
func (r *Repo) Create(ctx context.Context, tx *sql.Tx, u User) error {
	return errors.New("auth.Repo.Create: not implemented")
}

// GetByID returns a user by primary key. TODO(port): Phase 4.
func (r *Repo) GetByID(ctx context.Context, id uuid.UUID) (User, error) {
	return User{}, ErrNotFound
}

// GetByUsername is used by login. TODO(port): Phase 4.
func (r *Repo) GetByUsername(ctx context.Context, username string) (User, error) {
	return User{}, ErrNotFound
}

// GetTokenVersion loads just the tv column for Verify's fast path.
// TODO(port): Phase 4.
func (r *Repo) GetTokenVersion(ctx context.Context, id uuid.UUID) (int, error) {
	return 0, ErrNotFound
}

// IncrementTokenVersion implements /api/auth/logout-all.
// TODO(port): Phase 4.
func (r *Repo) IncrementTokenVersion(ctx context.Context, id uuid.UUID) error {
	return errors.New("auth.Repo.IncrementTokenVersion: not implemented")
}

// Delete removes a user row. FK cascade cleans up every dependent row.
// Callers should emit a UserDeleted outbox event in the same transaction.
// TODO(port): Phase 4.
func (r *Repo) Delete(ctx context.Context, tx *sql.Tx, id uuid.UUID) error {
	return errors.New("auth.Repo.Delete: not implemented")
}
