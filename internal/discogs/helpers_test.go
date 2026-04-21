package discogs_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/macabc/muzika/internal/db"
)

// newTempDB opens a fresh SQLite file in t.TempDir() with the full migration
// chain applied. Needed for tests that exercise the 30-day response cache
// (which reads/writes discogs_cache).
func newTempDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "muzika-test.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := db.MigrateEmbedded(d); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return d
}
