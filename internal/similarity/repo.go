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

// SeedsFor returns the user's current similar-mode seed set.
// Empty slice = similar mode is off (user has no seeds queued).
// Order is stable across calls (lexicographic on song UUID) —
// matters because the singular seedSongId API field and the
// random per-refill pick both derive from this list and both
// should behave deterministically against a given set.
func (r *Repo) SeedsFor(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	if r == nil || r.db == nil {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT song_id FROM user_similarity_seeds
		 WHERE user_id = ? ORDER BY song_id`,
		userID.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("similarity: list seeds: %w", err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, fmt.Errorf("similarity: scan seed: %w", err)
		}
		id, err := uuid.Parse(s)
		if err != nil {
			continue // defensive: skip malformed rows rather than fail
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// AddSeed appends a song to the user's seed set. Idempotent —
// the PRIMARY KEY on (user_id, song_id) means a duplicate INSERT
// is silently ignored. No-op on uuid.Nil.
func (r *Repo) AddSeed(ctx context.Context, userID, songID uuid.UUID) error {
	if r == nil || r.db == nil || songID == uuid.Nil {
		return nil
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO user_similarity_seeds (user_id, song_id)
		 VALUES (?, ?)
		 ON CONFLICT(user_id, song_id) DO NOTHING`,
		userID.String(), songID.String(),
	)
	if err != nil {
		return fmt.Errorf("similarity: add seed: %w", err)
	}
	return nil
}

// RemoveSeed drops one song from the seed set. No-op when the
// seed wasn't in the set — no error, idempotent shape matches
// AddSeed.
func (r *Repo) RemoveSeed(ctx context.Context, userID, songID uuid.UUID) error {
	if r == nil || r.db == nil || songID == uuid.Nil {
		return nil
	}
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM user_similarity_seeds
		 WHERE user_id = ? AND song_id = ?`,
		userID.String(), songID.String(),
	)
	if err != nil {
		return fmt.Errorf("similarity: remove seed: %w", err)
	}
	return nil
}

// ReplaceSeeds atomically overwrites the user's seed set with
// `songIDs`. Pass nil or an empty slice to clear similar mode.
// Duplicates in the input are tolerated (UNION the list before
// INSERT).
//
// Atomic via a transaction so a partial failure can't leave the
// set in a half-migrated state — the singular "set seedSongId"
// API shape (backward compat) routes through here.
func (r *Repo) ReplaceSeeds(ctx context.Context, userID uuid.UUID, songIDs []uuid.UUID) error {
	if r == nil || r.db == nil {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("similarity: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // harmless after Commit

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM user_similarity_seeds WHERE user_id = ?`,
		userID.String(),
	); err != nil {
		return fmt.Errorf("similarity: clear seeds: %w", err)
	}
	seen := make(map[uuid.UUID]struct{}, len(songIDs))
	for _, id := range songIDs {
		if id == uuid.Nil {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO user_similarity_seeds (user_id, song_id)
			 VALUES (?, ?)`,
			userID.String(), id.String(),
		); err != nil {
			return fmt.Errorf("similarity: insert seed: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("similarity: commit seeds: %w", err)
	}
	return nil
}

// SeedFor returns the first element of the user's seed set, or
// uuid.Nil when the set is empty. Preserved for the singular
// backward-compat API field (similar-mode GET's seedSongId) and
// for callers that need a single representative seed.
//
// Order matches SeedsFor's stable sort — two calls return the
// same "first" against an unchanged set.
func (r *Repo) SeedFor(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	if r == nil || r.db == nil {
		return uuid.Nil, nil
	}
	var raw string
	err := r.db.QueryRowContext(ctx,
		`SELECT song_id FROM user_similarity_seeds
		 WHERE user_id = ? ORDER BY song_id LIMIT 1`,
		userID.String(),
	).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("similarity: read first seed: %w", err)
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, nil
	}
	return id, nil
}

// SetSeed replaces the seed set with a single song (or clears
// when seedSongID is uuid.Nil). Kept as a compat wrapper over
// ReplaceSeeds so the v0.5 handler's "set one seed" behavior
// still works unchanged during the frontend transition.
func (r *Repo) SetSeed(ctx context.Context, userID, seedSongID uuid.UUID) error {
	if seedSongID == uuid.Nil {
		return r.ReplaceSeeds(ctx, userID, nil)
	}
	return r.ReplaceSeeds(ctx, userID, []uuid.UUID{seedSongID})
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
