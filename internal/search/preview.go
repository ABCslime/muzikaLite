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
// v0.4.2 PR B: returns four sections (genres, artists, releases, labels)
// so the dropdown can categorize results instead of mashing everything
// into one list. Releases is the current Preview output; artists + labels
// fire Discogs' type=artist / type=label endpoints in parallel with the
// release call; genres are matched client-side against Discogs' closed
// vocabulary (no API call).
package search

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"github.com/macabc/muzika/internal/discogs"
)

// defaultPerCategoryLimit bounds each section's row count. 5 per section
// keeps the dropdown to roughly one screen without scrolling; all four
// sections full-height renders 20 rows.
const defaultPerCategoryLimit = 5

// v0.4.2 PR B.1: the canonical genre + style vocabulary lives in
// internal/discogs.GenreVocabulary(). Matching it is how we surface
// suggestions like "House" / "Techno" / "Trance" without needing the
// Discogs API to answer the question.

// Candidate is a release row — kept for backward compatibility with the
// (now-legacy) shape the client used before PR B.
type Candidate struct {
	Title         string `json:"title"`
	Artist        string `json:"artist"`
	CatalogNumber string `json:"catalogNumber,omitempty"`
	Year          int    `json:"year,omitempty"`
}

// Entity is the JSON payload for an artist or label hit.
type Entity struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Thumb string `json:"thumb,omitempty"`
}

// Preview is the v0.4.2 PR B multi-category response shape. Each
// section is always present (serialized as an empty array when the
// category has no hits, never null) so the frontend can render the
// section headers unconditionally.
type Preview struct {
	Genres   []string    `json:"genres"`
	Artists  []Entity    `json:"artists"`
	Releases []Candidate `json:"releases"`
	Labels   []Entity    `json:"labels"`
}

// Previewer wraps the Discogs client for the preview endpoint.
type Previewer struct {
	client *discogs.Client
	log    *slog.Logger
}

// NewPreviewer constructs a Previewer. A nil *discogs.Client is valid —
// Preview returns (Preview{}, ErrDiscogsDisabled) so the handler can map
// it to 503.
func NewPreviewer(c *discogs.Client) *Previewer {
	return &Previewer{
		client: c,
		log:    slog.Default().With("mod", "search"),
	}
}

// ErrDiscogsDisabled signals that the preview endpoint can't serve
// because the Discogs integration isn't wired.
var ErrDiscogsDisabled = errDiscogsDisabled{}

type errDiscogsDisabled struct{}

func (errDiscogsDisabled) Error() string { return "search: Discogs not configured" }

// Preview fans out three Discogs calls (releases, artists, labels) in
// parallel and returns the union under a Preview struct. Genre matches
// run client-side against discogsGenres — no network call, case-
// insensitive substring match.
//
// Partial failure policy: if one of the Discogs calls errors, its
// section comes back empty but the others still populate. We only
// propagate an error if ALL three failed; that lets a transient
// rate-limit on one endpoint not blank the whole dropdown.
func (p *Previewer) Preview(ctx context.Context, query string) (Preview, error) {
	if p.client == nil {
		return Preview{}, ErrDiscogsDisabled
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return emptyPreview(), nil
	}

	out := Preview{
		Genres:   matchGenres(q),
		Artists:  []Entity{},
		Releases: []Candidate{},
		Labels:   []Entity{},
	}

	var (
		wg          sync.WaitGroup
		relErr      error
		artErr      error
		labErr      error
		releases    []discogs.SearchResult
		artists     []discogs.Entity
		labels      []discogs.Entity
	)

	wg.Add(3)
	go func() {
		defer wg.Done()
		releases, relErr = p.client.Preview(ctx, q, defaultPerCategoryLimit)
	}()
	go func() {
		defer wg.Done()
		artists, artErr = p.client.SearchArtists(ctx, q, defaultPerCategoryLimit)
	}()
	go func() {
		defer wg.Done()
		labels, labErr = p.client.SearchLabels(ctx, q, defaultPerCategoryLimit)
	}()
	wg.Wait()

	// Partial-failure policy.
	if relErr != nil && artErr != nil && labErr != nil {
		// All three failed — surface the release error (most common path).
		return Preview{}, relErr
	}
	if relErr != nil {
		p.log.Warn("preview: releases failed (other sections still returned)",
			"err", relErr, "query", q)
	}
	if artErr != nil {
		p.log.Warn("preview: artists failed (other sections still returned)",
			"err", artErr, "query", q)
	}
	if labErr != nil {
		p.log.Warn("preview: labels failed (other sections still returned)",
			"err", labErr, "query", q)
	}

	for _, r := range releases {
		out.Releases = append(out.Releases, Candidate{
			Title:         r.Title,
			Artist:        r.Artist,
			CatalogNumber: r.CatalogNumber,
			Year:          r.Year,
		})
	}
	for _, a := range artists {
		out.Artists = append(out.Artists, Entity{ID: a.ID, Name: a.Name, Thumb: a.Thumb})
	}
	for _, l := range labels {
		out.Labels = append(out.Labels, Entity{ID: l.ID, Name: l.Name, Thumb: l.Thumb})
	}
	return out, nil
}

// matchGenres returns names from the curated Discogs vocabulary whose
// name contains q (case-insensitive substring). Genres AND styles —
// "House", "Techno", "Trance" live in the same list alongside
// "Electronic" and "Rock". The Discogs client later routes each
// entry to the right query param based on KindOf(name).
//
// Cap prevents a short prefix like "a" from returning 20+ rows that
// blow out the dropdown.
func matchGenres(q string) []string {
	qLower := strings.ToLower(q)
	entries := discogs.GenreVocabulary()
	out := make([]string, 0, 8)
	const maxGenreSuggestions = 8
	for _, e := range entries {
		if strings.Contains(strings.ToLower(e.Name), qLower) {
			out = append(out, e.Name)
			if len(out) >= maxGenreSuggestions {
				break
			}
		}
	}
	return out
}

// emptyPreview returns a Preview with zero-length (but non-nil) slices
// in every field, so the JSON response is `{"genres":[],"artists":[],…}`
// rather than nulls. The frontend treats empty arrays as "hide section".
func emptyPreview() Preview {
	return Preview{
		Genres:   []string{},
		Artists:  []Entity{},
		Releases: []Candidate{},
		Labels:   []Entity{},
	}
}
