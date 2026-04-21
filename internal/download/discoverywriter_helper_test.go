package download_test

import (
	"database/sql"

	"github.com/macabc/muzika/internal/discovery"
)

// newDiscoveryWriter returns a real writer bound to db, for tests that
// exercise the discovery_log integration path. Lives in a separate _test.go
// so imports are ring-fenced — production code never constructs a writer
// directly in the download package.
func newDiscoveryWriter(db *sql.DB) *discovery.Writer {
	return discovery.NewWriter(db)
}
