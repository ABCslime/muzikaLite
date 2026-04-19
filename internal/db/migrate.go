package db

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

// Migrate runs every pending migration from `sourceDir` against db.
// Callers pass "file:///abs/path/to/migrations" as sourceDir.
func Migrate(db *sql.DB, sourceDir string) error {
	drv, err := sqlite.WithInstance(db, &sqlite.Config{})
	if err != nil {
		return fmt.Errorf("migrate: driver: %w", err)
	}
	m, err := migrate.NewWithDatabaseInstance(sourceDir, "sqlite", drv)
	if err != nil {
		return fmt.Errorf("migrate: init: %w", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate: up: %w", err)
	}
	return nil
}
