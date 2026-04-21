// Package preferences owns the per-user genre preference tables
// (user_bandcamp_tags, user_discogs_genres) introduced in migration 0005.
//
// Two normalized tables rather than one JSON column: cheaper cascading
// deletes under ON DELETE CASCADE, trivial "all users who follow X" lookups
// for v0.5 similarity, and no JSON parsing in Go.
//
// v0.4.1 PR 4.1.A.
package preferences

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/db"
)

// Repo persists user_bandcamp_tags + user_discogs_genres.
type Repo struct{ db *sql.DB }

func NewRepo(d *sql.DB) *Repo { return &Repo{db: d} }

// Preferences is the round-trip shape: one user's picked Bandcamp tags
// and Discogs genres. Empty slices mean "no preference" — the refiller
// will fall back to the .env defaults for that source.
type Preferences struct {
	BandcampTags  []string `json:"bandcampTags"`
	DiscogsGenres []string `json:"discogsGenres"`
}

// Get returns the current preferences for userID. Missing rows yield an
// empty Preferences, not an error.
func (r *Repo) Get(ctx context.Context, userID uuid.UUID) (Preferences, error) {
	out := Preferences{}
	tags, err := r.selectColumn(ctx, "user_bandcamp_tags", "tag", userID)
	if err != nil {
		return Preferences{}, err
	}
	out.BandcampTags = tags
	genres, err := r.selectColumn(ctx, "user_discogs_genres", "genre", userID)
	if err != nil {
		return Preferences{}, err
	}
	out.DiscogsGenres = genres
	return out, nil
}

// Replace overwrites userID's preferences with p (both lists). Atomic —
// the old rows are deleted and the new ones inserted in one transaction
// so a partial failure can't leave a half-updated preference set.
func (r *Repo) Replace(ctx context.Context, userID uuid.UUID, p Preferences) error {
	return db.WithTx(ctx, r.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM user_bandcamp_tags  WHERE user_id = ?`, userID.String()); err != nil {
			return fmt.Errorf("clear bandcamp tags: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM user_discogs_genres WHERE user_id = ?`, userID.String()); err != nil {
			return fmt.Errorf("clear discogs genres: %w", err)
		}
		for _, t := range p.BandcampTags {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO user_bandcamp_tags (user_id, tag) VALUES (?, ?)`,
				userID.String(), t); err != nil {
				return fmt.Errorf("insert bandcamp tag %q: %w", t, err)
			}
		}
		for _, g := range p.DiscogsGenres {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO user_discogs_genres (user_id, genre) VALUES (?, ?)`,
				userID.String(), g); err != nil {
				return fmt.Errorf("insert discogs genre %q: %w", g, err)
			}
		}
		return nil
	})
}

func (r *Repo) selectColumn(ctx context.Context, table, col string, userID uuid.UUID) ([]string, error) {
	// Table name is compile-time; no injection surface.
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+col+` FROM `+table+` WHERE user_id = ? ORDER BY `+col,
		userID.String())
	if err != nil {
		return nil, fmt.Errorf("select %s: %w", table, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
