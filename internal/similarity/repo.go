package similarity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// Repo persists per-user similar-mode state in the
// user_similarity_settings table (migration 0008).
//
// Single row per user. seed_song_id NULL = similar mode off; the
// refiller falls through to the existing genre-random path.
type Repo struct{ db *sql.DB }

// NewRepo wires a Repo. Pass nil for tests that stub the storage.
func NewRepo(d *sql.DB) *Repo { return &Repo{db: d} }

// SeedFor returns the user's currently active similar-mode seed
// song id, or uuid.Nil if similar mode is off (no row OR NULL
// seed_song_id). No-row and NULL-seed are deliberately collapsed
// — the frontend doesn't care about the difference, and treating
// them uniformly keeps the upsert simpler.
func (r *Repo) SeedFor(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	if r == nil || r.db == nil {
		return uuid.Nil, nil
	}
	var raw sql.NullString
	err := r.db.QueryRowContext(ctx,
		`SELECT seed_song_id FROM user_similarity_settings WHERE user_id = ?`,
		userID.String(),
	).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("similarity: read seed: %w", err)
	}
	if !raw.Valid || raw.String == "" {
		return uuid.Nil, nil
	}
	id, err := uuid.Parse(raw.String)
	if err != nil {
		return uuid.Nil, nil
	}
	return id, nil
}

// SetSeed sets (or clears with uuid.Nil) the user's similar-mode
// seed. Upsert — one row per user, no history. Clearing on a row
// that doesn't exist is a no-op (the SET NULL on a missing key is
// the same observed state as no row).
func (r *Repo) SetSeed(ctx context.Context, userID, seedSongID uuid.UUID) error {
	if r == nil || r.db == nil {
		return nil
	}
	var seedArg any
	if seedSongID == uuid.Nil {
		seedArg = nil
	} else {
		seedArg = seedSongID.String()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO user_similarity_settings (user_id, seed_song_id)
		VALUES (?, ?)
		ON CONFLICT(user_id) DO UPDATE SET seed_song_id = excluded.seed_song_id`,
		userID.String(), seedArg,
	)
	if err != nil {
		return fmt.Errorf("similarity: set seed: %w", err)
	}
	return nil
}
