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
	"time"

	"github.com/macabc/muzika/internal/discogs"
	"github.com/macabc/muzika/internal/filematch"
	"github.com/macabc/muzika/internal/soulseek"
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
//
// v0.4.2 PR D: now also holds a soulseek.Client for the bulk
// availability-probe path. Kept optional — nil is tolerated and the
// availability endpoint returns ErrSoulseekDisabled.
type Previewer struct {
	client   *discogs.Client
	soulseek soulseek.Client
	log      *slog.Logger
}

// NewPreviewer constructs a Previewer. A nil *discogs.Client is valid —
// Preview returns (Preview{}, ErrDiscogsDisabled) so the handler can map
// it to 503. Soulseek client defaults to nil; wire it via WithSoulseek
// for the v0.4.2 PR D availability-probe endpoint.
func NewPreviewer(c *discogs.Client) *Previewer {
	return &Previewer{
		client: c,
		log:    slog.Default().With("mod", "search"),
	}
}

// WithSoulseek wires a soulseek.Client onto the Previewer. Returns the
// receiver so main.go can chain it at construction time. Optional —
// availability requests return ErrSoulseekDisabled when unset.
func (p *Previewer) WithSoulseek(sk soulseek.Client) *Previewer {
	p.soulseek = sk
	return p
}

// ErrDiscogsDisabled signals that the preview endpoint can't serve
// because the Discogs integration isn't wired.
var ErrDiscogsDisabled = errDiscogsDisabled{}

type errDiscogsDisabled struct{}

func (errDiscogsDisabled) Error() string { return "search: Discogs not configured" }

// ErrSoulseekDisabled mirrors ErrDiscogsDisabled for the availability
// probe — returned when main.go didn't wire a soulseek.Client onto
// the Previewer (tests, offline dev runs).
var ErrSoulseekDisabled = errSoulseekDisabled{}

type errSoulseekDisabled struct{}

func (errSoulseekDisabled) Error() string { return "search: Soulseek not configured" }

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

// ---- v0.4.2 PR C — artist / label / release detail views ----------------

// ArtistDetail is the payload for /api/discogs/artist/{id}. The UI
// renders Name at the top, Releases as a grid with a "Queue" button
// that routes through the normal search-acquire path.
type ArtistDetail struct {
	ID       int         `json:"id"`
	Name     string      `json:"name"`
	Releases []Candidate `json:"releases"`
}

// LabelDetail is the label analog of ArtistDetail.
type LabelDetail struct {
	ID       int         `json:"id"`
	Name     string      `json:"name"`
	Releases []Candidate `json:"releases"`
}

// ReleaseDetail is the payload for /api/discogs/release/{id}. Carries
// enough metadata to re-acquire the release plus a tracklist for
// display. "Add album to queue" on the frontend calls the existing
// search-acquire path with {title, artist, catalogNumber}.
type ReleaseDetail struct {
	ID            int     `json:"id"`
	Title         string  `json:"title"`
	Artist        string  `json:"artist"`
	Year          int     `json:"year,omitempty"`
	CatalogNumber string  `json:"catalogNumber,omitempty"`
	Label         string  `json:"label,omitempty"`
	Tracks        []Track `json:"tracks"`
}

// Track is one row in a release's tracklist.
type Track struct {
	Position string `json:"position,omitempty"`
	Title    string `json:"title"`
	Duration string `json:"duration,omitempty"`
}

// Artist fetches the artist detail + their releases. The artist name
// comes from the FIRST release's "artist" field since Discogs doesn't
// return a direct name field on the /artists/{id}/releases endpoint;
// that's usually correct for single-artist IDs. For collaboration IDs
// where releases credit multiple artists, the first-release heuristic
// shows "A & B" which is acceptable.
func (p *Previewer) Artist(ctx context.Context, id int) (ArtistDetail, error) {
	if p.client == nil {
		return ArtistDetail{}, ErrDiscogsDisabled
	}
	releases, err := p.client.ArtistReleases(ctx, id, 50)
	if err != nil {
		return ArtistDetail{}, err
	}
	out := ArtistDetail{ID: id, Releases: make([]Candidate, 0, len(releases))}
	for _, r := range releases {
		out.Releases = append(out.Releases, Candidate{
			Title:         r.Title,
			Artist:        r.Artist,
			CatalogNumber: r.CatalogNumber,
			Year:          r.Year,
		})
		if out.Name == "" && r.Artist != "" {
			out.Name = r.Artist
		}
	}
	return out, nil
}

// Label is the label analog of Artist.
func (p *Previewer) Label(ctx context.Context, id int) (LabelDetail, error) {
	if p.client == nil {
		return LabelDetail{}, ErrDiscogsDisabled
	}
	releases, err := p.client.LabelReleases(ctx, id, 50)
	if err != nil {
		return LabelDetail{}, err
	}
	out := LabelDetail{ID: id, Releases: make([]Candidate, 0, len(releases))}
	for _, r := range releases {
		out.Releases = append(out.Releases, Candidate{
			Title:         r.Title,
			Artist:        r.Artist,
			CatalogNumber: r.CatalogNumber,
			Year:          r.Year,
		})
	}
	// Label Name isn't on per-release rows. Leave blank; the frontend
	// either shows the ID or falls back to a search/suggestion. A
	// future PR could add a /labels/{id} lookup to enrich this.
	return out, nil
}

// ---- v0.4.2 PR D: bulk availability probe -------------------------------

// AvailabilityQuery is one row to probe — same tuple shape we use for
// searchAcquire so the frontend can reuse what it already has.
// Title + Artist required; CatalogNumber optional.
type AvailabilityQuery struct {
	Title         string `json:"title"`
	Artist        string `json:"artist"`
	CatalogNumber string `json:"catalogNumber,omitempty"`
}

// AvailabilityResult mirrors AvailabilityQuery order from the request.
// Available is true iff Soulseek returned at least one peer within
// availabilityProbeWindow. The probe is "any peer?" — the gate/ladder
// still run on actual Queue click, so Available=true doesn't guarantee
// a quality-floor-passing download.
//
// PeerCount is the raw response count (pre-gate) for debugging and
// potential future UI ("12 peers found").
type AvailabilityResult struct {
	Available bool `json:"available"`
	PeerCount int  `json:"peerCount,omitempty"`
}

// availabilityProbeWindow is shorter than the download worker's
// 5 s probe — bulk pages want snappy feedback more than exhaustive
// coverage. Combined with the 10-way concurrency below, a 20-item
// artist page resolves in ~4 s worst case.
const availabilityProbeWindow = 2 * time.Second

// availabilityConcurrency caps goroutine fan-out. gosk's in-flight
// search registry is cheap but we don't want to blast the Soulseek
// server with 50 simultaneous searches from a single label page.
const availabilityConcurrency = 10

// CheckAvailability probes Soulseek for every item in `items` in
// parallel (capped at availabilityConcurrency) and returns results
// in input order. Empty input returns an empty slice without any
// network calls.
//
// Per-item errors collapse to Available=false — a probe that failed
// (network glitch, gosk busy) looks the same to the UI as "no peers
// have this". The user can still click Queue to get a full-ladder
// retry with better signal.
func (p *Previewer) CheckAvailability(ctx context.Context, items []AvailabilityQuery) ([]AvailabilityResult, error) {
	if p.soulseek == nil {
		return nil, ErrSoulseekDisabled
	}
	if len(items) == 0 {
		return []AvailabilityResult{}, nil
	}

	results := make([]AvailabilityResult, len(items))
	sem := make(chan struct{}, availabilityConcurrency)
	var wg sync.WaitGroup

	for i, it := range items {
		query := bulkProbeQuery(it)
		if query == "" {
			// Nothing to probe on; mark unavailable without a Soulseek call.
			results[i] = AvailabilityResult{Available: false}
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, q string) {
			defer wg.Done()
			defer func() { <-sem }()
			res, err := p.soulseek.Search(ctx, q, availabilityProbeWindow)
			if err != nil {
				p.log.Debug("availability probe failed",
					"query", q, "err", err)
				return // leaves Available=false zero value
			}
			results[idx] = AvailabilityResult{
				Available: len(res) > 0,
				PeerCount: len(res),
			}
		}(i, query)
	}
	wg.Wait()
	return results, nil
}

// ---- v0.4.2 PR E: artist-broad availability -----------------------------

// artistBroadWindow is longer than availabilityProbeWindow because ONE
// search now carries an entire page's worth of signal; we'd rather
// pay the wall time once than N times. 5 s matches the download
// worker's probe window — if the artist has any Soulseek presence,
// results arrive well within that.
const artistBroadWindow = 5 * time.Second

// ArtistAvailabilityQuery is the PR E request shape. The frontend
// passes the artist name once plus the list of titles to test; the
// backend runs a single Soulseek search and filters client-side.
type ArtistAvailabilityQuery struct {
	Artist string   `json:"artist"`
	Titles []string `json:"titles"`
}

// CheckByArtistAvailability runs ONE Soulseek search for `artist` and
// reports which of `titles` appear among the returned filenames.
//
// Why this over CheckAvailability? Two reasons:
//
//  1. Efficiency — a 20-release artist page hits Soulseek once
//     instead of 20 times. One longer window produces more hits
//     than twenty short parallel ones against a jittery network.
//
//  2. Reliability — per-release probes use the literal title, which
//     loses on filename variance ("Song (Remastered).flac" vs
//     "Song"). Here we tokenize via filematch and match as a
//     word-set, so parens, punctuation, and stopword differences
//     don't cause false negatives.
//
// Returns one AvailabilityResult per title, in input order. PeerCount
// is the count of filtered filename matches for THIS title — not the
// raw search result count. Empty artist or no titles short-circuits
// to all-zero results without hitting Soulseek.
func (p *Previewer) CheckByArtistAvailability(ctx context.Context, artist string, titles []string) ([]AvailabilityResult, error) {
	if p.soulseek == nil {
		return nil, ErrSoulseekDisabled
	}
	artist = strings.TrimSpace(artist)
	results := make([]AvailabilityResult, len(titles))
	if artist == "" || len(titles) == 0 {
		return results, nil
	}

	soulseekResults, err := p.soulseek.Search(ctx, artist, artistBroadWindow)
	if err != nil {
		p.log.Debug("artist-broad availability search failed",
			"artist", artist, "err", err)
		return results, nil // all unavailable; not a hard error
	}
	if len(soulseekResults) == 0 {
		return results, nil
	}

	// Per-title token match against each filename. The filename set is
	// small (tens to low hundreds) so O(titles × results) is fine.
	for i, title := range titles {
		tokens := filematch.Tokens(title)
		if len(tokens) == 0 {
			continue
		}
		count := 0
		for _, r := range soulseekResults {
			if filematch.Contains(r.Filename, tokens) {
				count++
			}
		}
		if count > 0 {
			results[i] = AvailabilityResult{Available: true, PeerCount: count}
		}
	}
	return results, nil
}

// bulkProbeQuery mirrors download's bestProbeQuery priority (artist+
// title first, then title, then catno) — the same reasoning about
// what Soulseek users actually tag applies.
func bulkProbeQuery(it AvailabilityQuery) string {
	artist := strings.TrimSpace(it.Artist)
	title := strings.TrimSpace(it.Title)
	if artist != "" && title != "" {
		return artist + " " + title
	}
	if title != "" {
		return title
	}
	return strings.TrimSpace(it.CatalogNumber)
}

// Release fetches a full release detail by ID: metadata + tracklist.
func (p *Previewer) Release(ctx context.Context, id int) (ReleaseDetail, error) {
	if p.client == nil {
		return ReleaseDetail{}, ErrDiscogsDisabled
	}
	r, err := p.client.Release(ctx, id)
	if err != nil {
		return ReleaseDetail{}, err
	}
	out := ReleaseDetail{
		ID:            r.ID,
		Title:         r.Title,
		Artist:        r.Artist,
		Year:          r.Year,
		CatalogNumber: r.CatalogNumber,
		Label:         r.Label,
		Tracks:        make([]Track, 0, len(r.Tracks)),
	}
	for _, t := range r.Tracks {
		out.Tracks = append(out.Tracks, Track{
			Position: t.Position,
			Title:    t.Title,
			Duration: t.Duration,
		})
	}
	return out, nil
}
