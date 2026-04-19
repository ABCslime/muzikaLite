// Package db owns the SQLite connection and its pragmas.
//
// We use SetMaxOpenConns(1): a single serialized connection. See CLAUDE.md
// for the rationale and the escape hatch (split read pool + single writer)
// if queuing ever shows up in request latency.
package db

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Open opens the SQLite file at path, applies the standard PRAGMAs, and
// returns a *sql.DB configured for single-connection use.
func Open(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("db: open %q: %w", path, err)
	}

	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA cache_size = -8000", // ~8 MB
		"PRAGMA temp_store = MEMORY",
	}
	for _, p := range pragmas {
		if _, err := db.ExecContext(context.Background(), p); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("db: %q: %w", p, err)
		}
	}

	// Serialized single-writer posture. See CLAUDE.md for the escape hatch.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	return db, nil
}
