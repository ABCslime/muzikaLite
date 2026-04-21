package discogs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// cacheTTL is the max age a cache row is considered fresh. ROADMAP §v0.4
// item 2 requires "30-day SQLite cache". Expired rows are hidden from
// readers but not deleted on read — a periodic sweep (Sweep) handles that.
const cacheTTL = 30 * 24 * time.Hour

// cache is a thin SQLite-backed key-value store over the discogs_cache
// table created in migration 0003. Values are raw response bodies (the
// JSON payload from api.discogs.com). Readers decide whether to decode.
type cache struct {
	db  *sql.DB
	now func() time.Time // swap in tests
}

func newCache(db *sql.DB) *cache {
	return &cache{db: db, now: time.Now}
}

// Get returns the cached payload for key if it exists and is fresher than
// cacheTTL. sql.ErrNoRows is returned for both missing and stale rows —
// callers treat them identically (fall through to a live HTTP call).
func (c *cache) Get(ctx context.Context, key string) ([]byte, error) {
	if c == nil || c.db == nil {
		return nil, sql.ErrNoRows
	}
	cutoff := c.now().Add(-cacheTTL).Unix()
	var payload []byte
	err := c.db.QueryRowContext(ctx,
		`SELECT payload FROM discogs_cache WHERE cache_key = ? AND created_at > ?`,
		key, cutoff).Scan(&payload)
	if err != nil {
		return nil, err
	}
	return payload, nil
}

// Put writes (or replaces) the cache row for key. Errors are surfaced so
// callers can log; a cache write failure shouldn't propagate as a search
// error, so the worker layer logs and continues.
func (c *cache) Put(ctx context.Context, key string, payload []byte) error {
	if c == nil || c.db == nil {
		return nil
	}
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO discogs_cache (cache_key, payload, created_at)
		VALUES (?, ?, ?)
		ON CONFLICT(cache_key) DO UPDATE
		  SET payload = excluded.payload,
		      created_at = excluded.created_at`,
		key, payload, c.now().Unix())
	if err != nil {
		return fmt.Errorf("discogs: cache put: %w", err)
	}
	return nil
}

// Sweep deletes rows older than cacheTTL. Call occasionally (e.g. once at
// startup) to cap table growth. Returns the number of rows removed.
func (c *cache) Sweep(ctx context.Context) (int64, error) {
	if c == nil || c.db == nil {
		return 0, nil
	}
	cutoff := c.now().Add(-cacheTTL).Unix()
	res, err := c.db.ExecContext(ctx,
		`DELETE FROM discogs_cache WHERE created_at <= ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("discogs: cache sweep: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		// modernc/sqlite supports RowsAffected, but defensively fall through.
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return n, nil
}
