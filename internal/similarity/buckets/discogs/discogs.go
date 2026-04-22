// Package discogs provides similarity.Bucket implementations
// backed by cached Discogs metadata. v0.5 ships five built-in
// buckets here; v0.6 plugin buckets live elsewhere.
//
// Each bucket is small (~30-50 lines) on purpose: Bucket
// implementations are the place we'll add new discovery angles
// over time, so keeping them readable matters more than DRY.
// Shared helpers (release → Candidate conversion, year-window
// filtering) live in this file.
package discogs

import (
	"strings"

	"github.com/macabc/muzika/internal/discogs"
	"github.com/macabc/muzika/internal/similarity"
)

// releasesToCandidates maps Discogs SearchResults into
// similarity.Candidates with confidence=1.0 (per-bucket weight
// carries the signal, individual confidence is uniform). Drops
// rows with empty title/artist defensively.
//
// Edge metadata (the v0.7 graph view's tooltips) is added by the
// caller — bucket-specific keys ("label","festival","year") don't
// belong here.
func releasesToCandidates(rs []discogs.SearchResult) []similarity.Candidate {
	out := make([]similarity.Candidate, 0, len(rs))
	for _, r := range rs {
		title := strings.TrimSpace(r.Title)
		artist := strings.TrimSpace(r.Artist)
		if title == "" || artist == "" {
			continue
		}
		out = append(out, similarity.Candidate{
			Title:    title,
			Artist:   artist,
			ImageURL: r.Thumb,
		})
	}
	return out
}

// withinYears keeps releases whose Year is within ±yearsWindow
// of seedYear. Releases with Year=0 (unknown) are kept — better
// to over-include than to drop a candidate over missing metadata.
func withinYears(rs []discogs.SearchResult, seedYear, yearsWindow int) []discogs.SearchResult {
	if seedYear <= 0 {
		return rs
	}
	out := make([]discogs.SearchResult, 0, len(rs))
	for _, r := range rs {
		if r.Year == 0 || abs(r.Year-seedYear) <= yearsWindow {
			out = append(out, r)
		}
	}
	return out
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
