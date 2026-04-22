package similarity

import (
	"context"
	"database/sql"
	"encoding/json"
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

// WeightsFor satisfies similarity.WeightStore. Returns the
// user's tuned per-bucket weights as a sparse map[bucketID]weight.
// A nil map (no row, NULL column, malformed JSON) makes the
// engine fall through to each bucket's DefaultWeight — critical
// invariant, v0.5 PR A relies on it for new users.
//
// Malformed JSON is logged-and-swallowed rather than surfaced:
// we'd rather run the engine on defaults than fail a refill
// cycle over a stored-value typo.
func (r *Repo) WeightsFor(ctx context.Context, userID uuid.UUID) (map[string]float64, error) {
	if r == nil || r.db == nil {
		return nil, nil
	}
	var raw sql.NullString
	err := r.db.QueryRowContext(ctx,
		`SELECT bucket_weights FROM user_similarity_settings WHERE user_id = ?`,
		userID.String(),
	).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("similarity: read weights: %w", err)
	}
	if !raw.Valid || raw.String == "" {
		return nil, nil
	}
	var out map[string]float64
	if err := json.Unmarshal([]byte(raw.String), &out); err != nil {
		// Malformed JSON in storage — treat as "no tuning." Don't
		// propagate the error; the engine must never stall on a
		// typo in the weights blob.
		return nil, nil
	}
	return out, nil
}

// SetWeights replaces the user's bucket_weights JSON. nil or
// empty map clears to NULL (pure defaults). Upsert so calling
// SetWeights on a user who hasn't toggled similar mode yet still
// lands a row and the weights persist for when they do.
//
// Coerces negative values to 0 — the engine already does this at
// read time, but storing the raw value would let UI roundtripping
// show -1s. Clean the data on the write side.
func (r *Repo) SetWeights(ctx context.Context, userID uuid.UUID, weights map[string]float64) error {
	if r == nil || r.db == nil {
		return nil
	}
	var weightsArg any
	if len(weights) == 0 {
		weightsArg = nil
	} else {
		cleaned := make(map[string]float64, len(weights))
		for k, v := range weights {
			if v < 0 {
				v = 0
			}
			cleaned[k] = v
		}
		raw, err := json.Marshal(cleaned)
		if err != nil {
			return fmt.Errorf("similarity: marshal weights: %w", err)
		}
		weightsArg = string(raw)
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO user_similarity_settings (user_id, bucket_weights)
		VALUES (?, ?)
		ON CONFLICT(user_id) DO UPDATE SET bucket_weights = excluded.bucket_weights`,
		userID.String(), weightsArg,
	)
	if err != nil {
		return fmt.Errorf("similarity: set weights: %w", err)
	}
	return nil
}
