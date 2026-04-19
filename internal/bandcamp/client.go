// Package bandcamp scrapes Bandcamp's discover endpoint to populate the song
// catalog when the queue runs low. Consumes RequestRandomSong, produces
// RequestSlskdSong. Genre is honored (replaces the old hardcoded "hisa").
//
// Phase 3 scaffold: interfaces and stubs only. Phase 7 ports business logic.
package bandcamp

import "errors"

// ErrNotImplemented marks scaffold stubs.
var ErrNotImplemented = errors.New("bandcamp: not implemented")

// Client wraps the HTTP traffic to bandcamp.com. Replaces the jsoup-based
// BandcampSearcher from the old Java service.
type Client struct {
	baseURL     string
	defaultTags []string
}

// NewClient constructs a Client with the configured default-tag fallback.
// Defaults ship from MUZIKA_BANDCAMP_DEFAULT_TAGS.
func NewClient(baseURL string, defaultTags []string) *Client {
	return &Client{baseURL: baseURL, defaultTags: defaultTags}
}
