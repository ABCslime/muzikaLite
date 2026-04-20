package db

import "strings"

// IsUniqueErr returns true if err is a SQLite UNIQUE-constraint violation.
// modernc.org/sqlite wraps errors with numeric codes; the text form is
// stable across releases, so string matching is fine here.
func IsUniqueErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "constraint failed: UNIQUE")
}
