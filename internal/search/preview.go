// Package search owns the user-facing typeahead preview surface:
// GET /api/queue/search/preview?q=... returns up to N Discogs candidates
// so the user can pick which release they actually want rather than being
// stuck with an auto-pick.
//
// Stateless. The only dependency is a *discogs.Client — we don't touch
// the DB, the bus, or queue state from this package. Acquisition
// (insert stub + publish RequestDownload) still lives in internal/queue
// so queue remains the single writer to queue_songs / queue_entries.
//
// v0.4.1 PR C.
package search

import (
	"context"
	"log/slog"
	"strings"

	"github.com/macabc/muzika/internal/discogs"
)

// defaultLimit bounds the dropdown size. 10 keeps the UI list readable
// and limits one preview call to a single Discogs API page (which returns
// up to 100; we don't need more).
const defaultLimit = 10

// Candidate is the JSON shape returned to the frontend. Mirrors
// discogs.SearchResult but lives in this package so the /api/queue/...
// contract doesn't leak internal type names to the client.
type Candidate struct {
	Title         string `json:"title"`
	Artist        string `json:"artist"`
	CatalogNumber string `json:"catalogNumber,omitempty"`
	Year          int    `json:"year,omitempty"`
}

// Previewer wraps the Discogs client for the preview endpoint.
type Previewer struct {
	client *discogs.Client
	log    *slog.Logger
}

// NewPreviewer constructs a Previewer. A nil *discogs.Client is valid —
// Preview returns (nil, ErrDiscogsDisabled) so the handler can map it to
// 503. This keeps the preview endpoint registrable whether or not
// Discogs is enabled in config.
func NewPreviewer(c *discogs.Client) *Previewer {
	return &Previewer{
		client: c,
		log:    slog.Default().With("mod", "search"),
	}
}

// ErrDiscogsDisabled signals that the preview endpoint can't serve
// because the Discogs integration isn't wired. Addresses Bug #1 from
// the v0.4.x QA walk-through — search previously leaked stubs silently
// when Discogs was disabled.
var ErrDiscogsDisabled = errDiscogsDisabled{}

type errDiscogsDisabled struct{}

func (errDiscogsDisabled) Error() string { return "search: Discogs not configured" }

// Preview returns up to defaultLimit candidates matching query. Empty
// query yields an empty slice (the caller renders "no dropdown").
// Discogs-level errors propagate so the handler can distinguish
// rate-limiting (429) from upstream failures (502).
func (p *Previewer) Preview(ctx context.Context, query string) ([]Candidate, error) {
	if p.client == nil {
		return nil, ErrDiscogsDisabled
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	results, err := p.client.Preview(ctx, q, defaultLimit)
	if err != nil {
		return nil, err
	}
	out := make([]Candidate, 0, len(results))
	for _, r := range results {
		out = append(out, Candidate{
			Title:         r.Title,
			Artist:        r.Artist,
			CatalogNumber: r.CatalogNumber,
			Year:          r.Year,
		})
	}
	return out, nil
}
