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
//
// v0.4.3: Thumb carries Discogs' small cover-art URL so the frontend
// can render album thumbnails in ReleaseGrid, ArtistView, LabelView.
// Often empty for rare pressings; frontend falls back to a gradient
// placeholder.
//
// v0.4.4: ID + IsAlbum populated from Discogs' release id and format
// string. The frontend uses ID to route to /album/{id} and the
// "Add album to playlist" endpoint; IsAlbum to bucket releases
// into the Album section (multi-track LP/EP/Album) versus the
// Single section (1-2 track single/EP).
type Candidate struct {
	ID            int    `json:"id,omitempty"`
	Title         string `json:"title"`
	Artist        string `json:"artist"`
	CatalogNumber string `json:"catalogNumber,omitempty"`
	Year          int    `json:"year,omitempty"`
	Thumb         string `json:"thumb,omitempty"`
	IsAlbum       bool   `json:"isAlbum,omitempty"`
}

// isAlbum reports whether a Discogs SearchResult should bucket into
// the Album section of the artist/label view. The source endpoint
// (/artists/{id}/releases, /database/search, …) sets r.IsAlbum
// directly when it can — that's the authoritative signal. Otherwise
// we fall back to parsing the format string for Album/LP/EP/Mini-
// Album tokens, the way /database/search rows describe themselves.
// v0.4.4.
func isAlbum(r discogs.SearchResult) bool {
	if r.IsAlbum {
		return true
	}
	return discogs.IsAlbumFormat(r.Format)
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
//
// v0.4.2 PR E (post-QA): also holds a short-TTL cache of broad
// artist-search results. Re-navigating to the same artist within
// the cache window skips Soulseek entirely — typical user pattern
// "artist → album → back to artist" is now instant instead of
// paying another 10 s broad search.
type Previewer struct {
	client   *discogs.Client
	soulseek soulseek.Client
	log      *slog.Logger

	broadCache   map[string]broadCacheEntry
	broadCacheMu sync.Mutex
}

// broadCacheEntry is one artist's cached Soulseek broad-search
// results. Results are the raw SearchResults; callers filter them
// against title variants. Entries expire at ExpiresAt.
type broadCacheEntry struct {
	results   []soulseek.SearchResult
	expiresAt time.Time
}

// broadCacheTTL is how long a cached broad-search result stays
// fresh. 60 s is long enough for the typical "artist → album →
// back" round trip but short enough that peer availability
// updates propagate within a minute of a refresh. Previously
// bumped to 10 min to paper over a session-lifetime bug in
// the Soulseek client where the second search returned zero
// because the TCP connection was tied to the first request's
// context; that bug is fixed upstream in internal/soulseek/
// native.go, so the cache can go back to its "speed up common
// back-and-forth nav, not hide broken-ness" role.
const broadCacheTTL = 60 * time.Second

// broadCacheMaxEntries caps the cache to bound memory. 64 artists
// is plenty for interactive browsing; once hit, the whole cache
// is pruned of expired entries, which in steady-state keeps it
// well under the limit.
const broadCacheMaxEntries = 64

// NewPreviewer constructs a Previewer. A nil *discogs.Client is valid —
// Preview returns (Preview{}, ErrDiscogsDisabled) so the handler can map
// it to 503. Soulseek client defaults to nil; wire it via WithSoulseek
// for the v0.4.2 PR D availability-probe endpoint.
func NewPreviewer(c *discogs.Client) *Previewer {
	return &Previewer{
		client:     c,
		log:        slog.Default().With("mod", "search"),
		broadCache: make(map[string]broadCacheEntry),
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
			ID:            r.ID,
			Title:         r.Title,
			Artist:        r.Artist,
			CatalogNumber: r.CatalogNumber,
			Year:          r.Year,
			Thumb:         r.Thumb,
			IsAlbum:       isAlbum(r),
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
//
// v0.4.3: Image is a representative cover-art URL for the hero
// block. Picked from the first release with non-empty Thumb —
// cheaper than a dedicated /artists/{id} lookup AND more visually
// relevant (an album cover beats a press photo). Empty when the
// artist has zero releases with artwork.
type ArtistDetail struct {
	ID       int         `json:"id"`
	Name     string      `json:"name"`
	Image    string      `json:"image,omitempty"`
	Releases []Candidate `json:"releases"`
}

// LabelDetail is the label analog of ArtistDetail.
type LabelDetail struct {
	ID       int         `json:"id"`
	Name     string      `json:"name"`
	Image    string      `json:"image,omitempty"`
	Releases []Candidate `json:"releases"`
}

// ReleaseDetail is the payload for /api/discogs/release/{id}. Carries
// enough metadata to re-acquire the release plus a tracklist for
// display. "Add album to queue" on the frontend calls the existing
// search-acquire path with {title, artist, catalogNumber}.
//
// v0.4.3: Thumb / Cover carry the Discogs cover-art URLs. Thumb is
// the small (~150 px) version used in lists; Cover is the first
// full-size image from the release's images array, rendered in the
// AlbumView hero. Either can be empty (rare pressings without
// artwork uploaded); frontend falls back to a gradient.
type ReleaseDetail struct {
	ID            int     `json:"id"`
	Title         string  `json:"title"`
	Artist        string  `json:"artist"`
	Year          int     `json:"year,omitempty"`
	CatalogNumber string  `json:"catalogNumber,omitempty"`
	Label         string  `json:"label,omitempty"`
	Thumb         string  `json:"thumb,omitempty"`
	Cover         string  `json:"cover,omitempty"`
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
	// Two parallel fetches — releases list + entity detail (image,
	// canonical name). Both are cached on the Discogs side so a
	// repeat visit pays nothing; the parallelism only matters for
	// cold loads.
	var (
		wg          sync.WaitGroup
		releases    []discogs.SearchResult
		releasesErr error
		entity      discogs.EntityDetail
		entityErr   error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		releases, releasesErr = p.client.ArtistReleases(ctx, id, 50)
	}()
	go func() {
		defer wg.Done()
		entity, entityErr = p.client.Artist(ctx, id)
	}()
	wg.Wait()

	if releasesErr != nil {
		return ArtistDetail{}, releasesErr
	}
	out := ArtistDetail{ID: id, Releases: make([]Candidate, 0, len(releases))}
	if entityErr == nil {
		// Canonical name + real press image when Discogs returned them.
		out.Name = entity.Name
		out.Image = entity.Image
	} else {
		p.log.Debug("artist entity fetch failed (using release-derived fallbacks)",
			"id", id, "err", entityErr)
	}
	for _, r := range releases {
		out.Releases = append(out.Releases, Candidate{
			ID:            r.ID,
			Title:         r.Title,
			Artist:        r.Artist,
			CatalogNumber: r.CatalogNumber,
			Year:          r.Year,
			Thumb:         r.Thumb,
			IsAlbum:       isAlbum(r),
		})
		// Fallbacks if /artists/{id} didn't return a name/image —
		// e.g. transient Discogs error, brand new artist record.
		if out.Name == "" && r.Artist != "" {
			out.Name = r.Artist
		}
		if out.Image == "" && r.Thumb != "" {
			out.Image = r.Thumb
		}
	}
	return out, nil
}

// Label is the label analog of Artist.
func (p *Previewer) Label(ctx context.Context, id int) (LabelDetail, error) {
	if p.client == nil {
		return LabelDetail{}, ErrDiscogsDisabled
	}
	// Same parallel pattern as Artist — releases + entity detail.
	var (
		wg          sync.WaitGroup
		releases    []discogs.SearchResult
		releasesErr error
		entity      discogs.EntityDetail
		entityErr   error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		releases, releasesErr = p.client.LabelReleases(ctx, id, 50)
	}()
	go func() {
		defer wg.Done()
		entity, entityErr = p.client.Label(ctx, id)
	}()
	wg.Wait()

	if releasesErr != nil {
		return LabelDetail{}, releasesErr
	}
	out := LabelDetail{ID: id, Releases: make([]Candidate, 0, len(releases))}
	if entityErr == nil {
		out.Name = entity.Name
		out.Image = entity.Image
	} else {
		p.log.Debug("label entity fetch failed (using release-derived fallbacks)",
			"id", id, "err", entityErr)
	}
	for _, r := range releases {
		out.Releases = append(out.Releases, Candidate{
			ID:            r.ID,
			Title:         r.Title,
			Artist:        r.Artist,
			CatalogNumber: r.CatalogNumber,
			Year:          r.Year,
			Thumb:         r.Thumb,
			IsAlbum:       isAlbum(r),
		})
		if out.Image == "" && r.Thumb != "" {
			out.Image = r.Thumb
		}
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

// availabilityConcurrency caps goroutine fan-out. 6 is the empirical
// sweet spot: stacking 10+ parallel searches against gosk's session
// wedged its response drain on subsequent requests, while 3-way
// wasn't enough to finish a 30-release label page inside the
// per-request deadline. 6 keeps gosk responsive AND clears a
// typical label page in ~10 s.
const availabilityConcurrency = 6

// availabilityTimeout bounds how long the per-item label probe can
// take end-to-end. At 6-way concurrency × 2 s window, a 30-release
// label page resolves in ~10 s; the 20 s deadline absorbs slow
// batches and response drain with room to spare.
const availabilityTimeout = 20 * time.Second

// artistFallbackConcurrency caps the per-title probe fan-out when
// the broad search came back thin. Deliberately low: the earlier
// 10-way version was saturating gosk's session and wedging
// subsequent requests. 2 is enough to parallelise a handful of
// holdouts without adding real pressure.
const artistFallbackConcurrency = 2

// artistFallbackMaxProbes caps the TOTAL number of per-title probes
// a single fallback pass may fire, regardless of how many titles
// went unresolved in the broad phase. A thin-broad artist can have
// 20+ misses; probing all of them at concurrency 2 with 2 s per
// probe takes 20+ s, which overruns the handler deadline and
// leaves every row showing "Not found" to the UI. Capping at 5
// lets us rescue the most-likely winners (first misses seen,
// which correlate with the start of the Discogs release list —
// usually the more popular titles) while staying inside the
// 15 s deadline.
const artistFallbackMaxProbes = 5

// artistThinBroadThreshold is the "broad was healthy" heuristic.
// If an artist search returned more than this many filenames, we
// trust the coverage — titles that didn't surface in that sample
// almost certainly aren't on Soulseek either, and firing N more
// probes would just waste gosk time. Below the threshold the
// per-title fallback runs on unresolved titles.
const artistThinBroadThreshold = 50

// searchGate caps concurrent gosk.Search calls across ALL
// availability endpoints and goroutines, process-wide. gosk /
// soul's session gets increasingly slow once many searches are
// in flight against it; past empirical testing showed that 5+
// simultaneous searches started wedging the session to the
// point where subsequent requests got 0 results even for
// popular artists. 4 is the empirical ceiling for consistent
// recall across artist + label navigation.
//
// Package-level (not per-Previewer) on purpose: this is a shared
// limit on gosk's resource, not on any logical preview
// subsystem. A background worker that grows to call gosk.Search
// in the future should route through gatedSearch too.
var searchGate = make(chan struct{}, 4)

// gatedSearch acquires a slot on searchGate before calling
// p.soulseek.Search, releasing it on return. Respects ctx: if
// the gate is full and ctx cancels while we wait, returns
// ctx.Err() without touching gosk. Callers already handle errors
// as "not available" so a gated-out probe looks the same as a
// zero-peer probe to the UI.
func (p *Previewer) gatedSearch(ctx context.Context, query string, window time.Duration) ([]soulseek.SearchResult, error) {
	select {
	case searchGate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-searchGate }()
	return p.soulseek.Search(ctx, query, window)
}

// CheckAvailability probes Soulseek for every item in `items` in
// parallel (capped at availabilityConcurrency) and returns results
// in input order. Empty input returns an empty slice without any
// network calls.
//
// Per-item errors collapse to Available=false — a probe that failed
// (network glitch, gosk busy) looks the same to the UI as "no peers
// have this". The user can still click Queue to get a full-ladder
// retry with better signal.
//
// v0.4.2 PR E: the per-item probe now applies the filematch filter
// to its Soulseek results — a query that drew back unrelated files
// (Soulseek fuzzy matching) no longer green-lights the UI as
// "available" just because any file came back. A hit has to carry
// every title token in its filename to count. Same semantics as the
// download worker's probe path — same precision story for LabelView
// (the one surface still on per-item).
func (p *Previewer) CheckAvailability(ctx context.Context, items []AvailabilityQuery) ([]AvailabilityResult, error) {
	if p.soulseek == nil {
		return nil, ErrSoulseekDisabled
	}
	if len(items) == 0 {
		return []AvailabilityResult{}, nil
	}

	// Overall timeout so a wedged gosk doesn't leave the HTTP
	// request hanging forever. Partial results (everything probed
	// before deadline) come back; anything still in flight falls
	// back to Available=false in the UI, which the user can still
	// queue manually.
	ctx, cancel := context.WithTimeout(ctx, availabilityTimeout)
	defer cancel()

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
		// Respect the deadline at the scheduler: if ctx is done,
		// don't enqueue more goroutines.
		select {
		case <-ctx.Done():
			continue
		default:
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, q, title string) {
			defer wg.Done()
			defer func() { <-sem }()
			// Belt-and-suspenders select so a stuck gosk.Search
			// doesn't block this goroutine past the deadline.
			// The leaked goroutine will eventually drain when
			// gosk recovers; the HTTP handler returns on time.
			type out struct {
				res []soulseek.SearchResult
				err error
			}
			done := make(chan out, 1)
			go func() {
				res, err := p.gatedSearch(ctx, q, availabilityProbeWindow)
				done <- out{res, err}
			}()
			select {
			case o := <-done:
				if o.err != nil {
					p.log.Debug("availability probe failed",
						"query", q, "err", o.err)
					return
				}
				filtered := filterFilenamesByTitle(o.res, title)
				results[idx] = AvailabilityResult{
					Available: len(filtered) > 0,
					PeerCount: len(filtered),
				}
			case <-ctx.Done():
				return
			}
		}(i, query, it.Title)
	}
	wg.Wait()
	return results, nil
}

// ---- v0.4.2 PR E: artist-broad availability -----------------------------

// artistBroadWindow gives the artist-wide search enough time to pull
// back a representative sample of what peers have on that artist.
// Longer than the per-item probe (2 s) because this ONE search is
// supposed to cover an entire artist page — we'd rather pay the wall
// time once than fan out 20 shallow probes. 10 s is empirically the
// sweet spot: past that gosk response volume stops growing
// materially, under it long-tail artists come back thin.
//
// No per-title fallback. An earlier hybrid version fired per-title
// probes for every broad-miss; that flooded gosk's connection pool
// and wedged subsequent requests. One long broad search is more
// reliable than a broad + many narrow ones.
const artistBroadWindow = 10 * time.Second

// ArtistAvailabilityQuery is the PR E request shape. The frontend
// passes the artist name once plus the list of titles to test; the
// backend runs a single Soulseek search and filters client-side.
type ArtistAvailabilityQuery struct {
	Artist string   `json:"artist"`
	Titles []string `json:"titles"`
}

// CheckByArtistAvailability resolves availability for a page's worth
// of titles under a single artist with ONE Soulseek search:
// search "<artist>", scan the returned filenames, mark every title
// whose variants appear in at least one filename as Available.
//
// One search (not N) for three reasons:
//
//   - Efficiency: a 20-release artist page pays one round-trip.
//
//   - Reliability: Soulseek's session deals well with occasional
//     long-window searches; it doesn't deal well with bursts of
//     many concurrent searches from the same client, which is what
//     a per-title fan-out looks like.
//
//   - Recall: a 10 s broad window pulls back more distinct peers
//     than 20 × 2 s narrow ones because peers respond to the
//     artist-tier search asynchronously — the window width dominates.
//
// Titles that produce no variants (all stopwords, empty) are left
// Available=false — no signal to match on.
//
// Returns one AvailabilityResult per title, in input order. PeerCount
// is the filtered match count (pre-gate). Soft errors (Soulseek
// unreachable, timeout) are logged and return all-unavailable — no
// hard error bubbles; the UI degrades to neutral rows which the
// user can still queue.
func (p *Previewer) CheckByArtistAvailability(ctx context.Context, artist string, titles []string) ([]AvailabilityResult, error) {
	if p.soulseek == nil {
		return nil, ErrSoulseekDisabled
	}
	artist = strings.TrimSpace(artist)
	results := make([]AvailabilityResult, len(titles))
	if artist == "" || len(titles) == 0 {
		return results, nil
	}

	// Cache-first path: re-navigating to the same artist within the
	// TTL window returns in microseconds, and keeps gosk's session
	// from accumulating stress that degrades later searches.
	//
	// Critical: we DO NOT re-run the fallback here. The miss path
	// runs fallback once on cache-miss; subsequent cache hits must
	// reuse the same cached broad plus its fallback-accumulated
	// results, not fire another 5 gosk.Search calls per visit.
	// Per-visit fallback firing was causing gosk stress to snowball
	// across a browsing session — every re-navigation added 5 more
	// in-flight searches that gosk couldn't drain, until the whole
	// session went to 0 results.
	if broad, ok := p.broadCacheGet(artist); ok {
		p.log.Debug("by-artist availability cache hit",
			"artist", artist, "broad_count", len(broad))
		p.applyBroad(broad, titles, results)
		return results, nil
	}
	p.log.Debug("by-artist availability cache miss",
		"artist", artist)

	broad := p.broadSearchOnce(ctx, artist)
	if len(broad) == 0 {
		return results, nil
	}

	// Cache the raw broad results; the per-title filter is cheap
	// and callers' title lists vary across pages (artist detail
	// shows all releases; album page asks about one title).
	p.broadCachePut(artist, broad)
	p.log.Debug("by-artist availability cache put",
		"artist", artist, "broad_count", len(broad))
	p.applyBroad(broad, titles, results)

	// Thin-broad fallback: if Soulseek returned a small sample for
	// the artist name, per-title probes on the still-unresolved
	// titles can rescue a few (peer file-sharing naming varies).
	// Above the threshold we trust broad coverage — firing more
	// probes would just waste gosk time and risk wedging the
	// session for the next request.
	if len(broad) < artistThinBroadThreshold {
		p.runPerTitleFallback(ctx, artist, titles, results)
	}
	return results, nil
}

// runPerTitleFallback fires a small number of parallel
// "<artist> <title>" probes against Soulseek for titles that the
// broad phase didn't resolve. Each probe's results are filtered
// through the same filematch variants, so a false-positive wrong-
// song hit doesn't slip through.
//
// Concurrency is artistFallbackConcurrency (2) — empirically the
// ceiling that keeps gosk's session responsive for the NEXT
// availability request. The belt-and-suspenders goroutine-select
// mirrors the broad-search pattern so ctx cancellation cuts off
// in-flight gosk.Search calls cleanly from the handler's side.
//
// Mutates `results` in place. Does NOT return an error: a failed
// probe just means we leave that title Available=false.
func (p *Previewer) runPerTitleFallback(ctx context.Context, artist string, titles []string, results []AvailabilityResult) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, artistFallbackConcurrency)
	probed := 0
	for i, title := range titles {
		if results[i].Available {
			continue
		}
		if strings.TrimSpace(title) == "" {
			continue
		}
		variants := filematch.TitleVariants(title)
		if len(variants) == 0 {
			continue
		}
		// Budget cap — leaves later misses as Not Found rather
		// than overrunning the handler deadline. See
		// artistFallbackMaxProbes.
		if probed >= artistFallbackMaxProbes {
			break
		}
		probed++
		// Acquire a fallback slot. The select form matters: if sem
		// is saturated and ctx has already fired, we bail instead
		// of blocking the for-loop past the handler deadline.
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		wg.Add(1)
		go func(idx int, t string, vars [][]string) {
			defer wg.Done()
			defer func() { <-sem }()
			queryTitle := t
			if len(vars) > 1 {
				if a, _, ok := strings.Cut(t, " / "); ok {
					queryTitle = strings.TrimSpace(a)
				}
			}
			query := strings.TrimSpace(artist + " " + queryTitle)
			type out struct {
				res []soulseek.SearchResult
				err error
			}
			done := make(chan out, 1)
			go func() {
				res, err := p.gatedSearch(ctx, query, availabilityProbeWindow)
				done <- out{res, err}
			}()
			select {
			case o := <-done:
				if o.err != nil {
					p.log.Debug("per-title fallback probe failed",
						"query", query, "err", o.err)
					return
				}
				count := 0
				for _, r := range o.res {
					if filematch.ContainsAny(r.Filename, vars) {
						count++
					}
				}
				if count > 0 {
					results[idx] = AvailabilityResult{Available: true, PeerCount: count}
				}
			case <-ctx.Done():
				return
			}
		}(i, title, variants)
	}
	wg.Wait()
}

// broadSearchOnce runs a single broad Soulseek search through the
// global gate, with the belt-and-suspenders goroutine-select so a
// stalled gosk.Search doesn't hold the handler past ctx.
// Returns nil on error, ctx cancel, or zero results — callers
// treat all three identically (no data, move on).
func (p *Previewer) broadSearchOnce(ctx context.Context, artist string) []soulseek.SearchResult {
	type outcome struct {
		res []soulseek.SearchResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := p.gatedSearch(ctx, artist, artistBroadWindow)
		done <- outcome{res, err}
	}()
	select {
	case o := <-done:
		if o.err != nil {
			p.log.Debug("artist-broad availability search failed",
				"artist", artist, "err", o.err)
			return nil
		}
		return o.res
	case <-ctx.Done():
		p.log.Debug("artist-broad availability search hit deadline",
			"artist", artist)
		return nil
	}
}

// applyBroad fills `results` in place: for each title, count how
// many filenames in `broad` match any of its variants. Extracted
// so the cache-hit and cache-miss paths share one loop body.
func (p *Previewer) applyBroad(broad []soulseek.SearchResult, titles []string, results []AvailabilityResult) {
	for i, title := range titles {
		variants := filematch.TitleVariants(title)
		if len(variants) == 0 {
			continue
		}
		count := 0
		for _, r := range broad {
			if filematch.ContainsAny(r.Filename, variants) {
				count++
			}
		}
		if count > 0 {
			results[i] = AvailabilityResult{Available: true, PeerCount: count}
		}
	}
}

// broadCacheGet returns the cached broad-search results for `artist`
// (case-insensitive) if the entry is still fresh; otherwise false.
func (p *Previewer) broadCacheGet(artist string) ([]soulseek.SearchResult, bool) {
	key := strings.ToLower(artist)
	p.broadCacheMu.Lock()
	defer p.broadCacheMu.Unlock()
	entry, ok := p.broadCache[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(entry.expiresAt) {
		delete(p.broadCache, key)
		return nil, false
	}
	return entry.results, true
}

// broadCachePut stores `results` under `artist` (case-insensitive)
// with a TTL. Evicts expired entries when the cache hits the size
// cap — simple pass, fine at 64 entries; a real LRU would be
// overkill for browsing-scale traffic.
func (p *Previewer) broadCachePut(artist string, results []soulseek.SearchResult) {
	key := strings.ToLower(artist)
	p.broadCacheMu.Lock()
	defer p.broadCacheMu.Unlock()
	if len(p.broadCache) >= broadCacheMaxEntries {
		now := time.Now()
		for k, e := range p.broadCache {
			if now.After(e.expiresAt) {
				delete(p.broadCache, k)
			}
		}
		// If all entries are still fresh, drop the newest entry's
		// neighbor to make room; we don't track access time.
		if len(p.broadCache) >= broadCacheMaxEntries {
			for k := range p.broadCache {
				delete(p.broadCache, k)
				break
			}
		}
	}
	p.broadCache[key] = broadCacheEntry{
		results:   results,
		expiresAt: time.Now().Add(broadCacheTTL),
	}
}

// filterFilenamesByTitle is the search-package twin of
// download.filterByTitle: drops Soulseek results whose filename
// doesn't match any variant of `title` (filematch.ContainsAny).
// A no-op when title produces no variants.
//
// Variants handle slash-separated Discogs titles ("A / B" splits,
// compilation joins) — a filename that matches just one side
// counts, because that's how peers actually share these releases
// (usually as the single track, not the full pack label).
func filterFilenamesByTitle(results []soulseek.SearchResult, title string) []soulseek.SearchResult {
	variants := filematch.TitleVariants(title)
	if len(variants) == 0 {
		return results
	}
	out := make([]soulseek.SearchResult, 0, len(results))
	for _, r := range results {
		if filematch.ContainsAny(r.Filename, variants) {
			out = append(out, r)
		}
	}
	return out
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
		Thumb:         r.Thumb,
		Cover:         r.Cover,
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
