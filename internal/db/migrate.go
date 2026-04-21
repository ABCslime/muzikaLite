package db

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// migrationsFS holds the .sql files baked into the binary. Keeping them
// here (vs /migrations at install time) means `muzika` is a truly single-
// file deploy — no runtime dependency on a sibling directory on disk.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// MigrateEmbedded runs every pending migration from the embedded FS
// against db. This is the path used by production (main.go) so neither
// the Pi nor a laptop needs a /migrations directory at runtime.
func MigrateEmbedded(db *sql.DB) error {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("migrate: sub fs: %w", err)
	}
	src, err := iofs.New(sub, ".")
	if err != nil {
		return fmt.Errorf("migrate: iofs source: %w", err)
	}
	return runMigrations(db, func(drv database.Driver) (*migrate.Migrate, error) {
		return migrate.NewWithInstance("iofs", src, "sqlite", drv)
	})
}

// Migrate runs every pending migration from `sourceDir` against db.
// Callers pass "file:///abs/path/to/migrations" as sourceDir. Retained
// for tests and local tooling that want to point at an on-disk copy.
func Migrate(db *sql.DB, sourceDir string) error {
	return runMigrations(db, func(drv database.Driver) (*migrate.Migrate, error) {
		return migrate.NewWithDatabaseInstance(sourceDir, "sqlite", drv)
	})
}

func runMigrations(db *sql.DB, init func(database.Driver) (*migrate.Migrate, error)) error {
	drv, err := sqlite.WithInstance(db, &sqlite.Config{})
	if err != nil {
		return fmt.Errorf("migrate: driver: %w", err)
	}
	m, err := init(drv)
	if err != nil {
		return fmt.Errorf("migrate: init: %w", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate: up: %w", err)
	}
	return nil
}
