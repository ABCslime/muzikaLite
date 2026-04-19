package db

import (
	"context"
	"database/sql"
	"fmt"
)

// WithTx runs fn inside a transaction. Commits on nil error, rolls back otherwise.
// Callers use this for any multi-statement write that must be atomic (e.g. the
// outbox-insert-plus-state-mutation pattern from ARCHITECTURE.md §4).
func WithTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
