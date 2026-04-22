// Package discogs is the second seeder. It consumes DiscoveryIntent
// (StrategyRandom only in PR 2; StrategySearch in PR 3) and produces
// RequestDownload with CatalogNumber populated so the download ladder's
// rung 0 has something to try.
//
// Integration shape:
//   - HTTP against api.discogs.com via a Personal Access Token
//   - Per-client token bucket: 1 token/s, 5 burst (Discogs' quota is
//     60/min authenticated; we cap well under it)
//   - 30-day SQLite cache (discogs_cache table, migration 0003) so we
//     never hit the API for data we've already fetched
//   - No uploads, no OAuth dance, no collection/wantlist writes — read-only.
//
// ROADMAP §v0.4 item 2 lives here.
package discogs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is api.discogs.com. Tests inject an httptest server URL.
const DefaultBaseURL = "https://api.discogs.com"

// DefaultUserAgent is required by Discogs' ToS. Tests override via WithUserAgent.
const DefaultUserAgent = "muzika/0.4 (+https://github.com/ABCslime/muzikaLite)"

// Client wraps the HTTP traffic to api.discogs.com. Safe for concurrent
// use by multiple worker goroutines sharing one rate limiter + cache.
type Client struct {
	baseURL       string
	token         string
	userAgent     string
	defaultGenres []string
	httpClient    *http.Client
	rng           *rand.Rand
	limiter       *tokenBucket
	cache         *cache
	log           *slog.Logger
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient overrides the default http.Client (useful in tests).
func WithHTTPClient(c *http.Client) Option {
	return func(d *Client) { d.httpClient = c }
}

// WithRand overrides the random source (useful in tests).
func WithRand(r *rand.Rand) Option {
	return func(d *Client) { d.rng = r }
}

// WithUserAgent overrides the User-Agent header (tests mostly).
func WithUserAgent(ua string) Option {
	return func(d *Client) { d.userAgent = ua }
}

// WithCache plugs a SQLite-backed response cache. Pass nil *sql.DB to disable.
func WithCache(db *sql.DB) Option {
	return func(d *Client) { d.cache = newCache(db) }
}

// WithLimiter overrides the rate limiter. Useful for tests that want an
// infinitely fast bucket.
func WithLimiter(maxTokens, refillPerSec float64) Option {
	return func(d *Client) { d.limiter = newTokenBucket(maxTokens, refillPerSec) }
}

// NewClient constructs a Client with sane defaults.
//
//   - baseURL: DefaultBaseURL if empty
//   - defaultGenres: fallback when a DiscoveryIntent.Genre is empty
//   - token: required (Personal Access Token); unauthenticated requests work
//     for some endpoints but limit to 25/min and don't get the User-Agent
//     goodwill boost. We always authenticate.
func NewClient(baseURL, token string, defaultGenres []string, opts ...Option) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	c := &Client{
		baseURL:       baseURL,
		token:         token,
		userAgent:     DefaultUserAgent,
		defaultGenres: defaultGenres,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		//nolint:gosec // G404: non-crypto random is fine for result selection
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
		// 1 token/s refill, 5 burst — headroom above our single-worker
		// worst case (~1 req per DiscoveryIntent) without coming near
		// the 60/min quota.
		limiter: newTokenBucket(5, 1),
		log:     slog.Default().With("mod", "discogs"),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// SearchResult is one Discogs release picked by Search.
//
// Year is populated from the Discogs response when available; 0 means
// unknown. Used by the preview dropdown (v0.4.1 PR C) so users can
// distinguish reissues of the same title.
type SearchResult struct {
	Title         string
	Artist        string
	CatalogNumber string // "" if the release has no catno
	Year          int    // 0 if unknown

	// Thumb is Discogs' small cover-art URL for this release. Often
	// empty when the release hasn't been linked to artwork in
	// Discogs (common for old catalog, rare pressings). Consumers
	// must tolerate empty — the frontend renders a gradient
	// placeholder in that case. v0.4.3.
	Thumb string

	// ID is the Discogs release ID for this row. Populated when the
	// source endpoint returns it (artist/label releases lists do;
	// /database/search does too). Required by callers that want to
	// route to /album/<id> or expand a release into its tracklist.
	// v0.4.4.
	ID int

	// Format is the comma-separated Discogs format string for this
	// release: "CD, Album", "12\", EP", "7\", Single", etc. Used
	// downstream to classify Album vs Single. v0.4.4.
	Format string

	// IsAlbum is true when this row should bucket into the Album
	// section of the artist/label view. Populated by
	// fetchArtistOrLabelReleases — true for Discogs master rows
	// (which stand in for the abstract album) and for non-master
	// rows whose format string carries an Album/LP/EP token. Kept
	// separate from Format because /artists/{id}/releases rarely
	// includes the token and masters would otherwise be lost. v0.4.4.
	IsAlbum bool
}

// Search picks one random release from the top page of /database/search
// filtered by genre (or style — see below). Respects the rate limiter
// and the 30-day cache.
//
// Used by the passive-refill path (StrategyRandom). For user-initiated
// text search (StrategySearch, v0.4 PR 3), see SearchQuery.
//
// v0.4.2 PR B.1: the param routes on KindOf(name). Top-level Discogs
// Genres (Electronic, Rock, …) go to `genre=`; Styles (House, Techno,
// Trance, …) go to `style=`. Using the wrong param returns zero
// results from Discogs, so this routing is load-bearing for any user
// who pins a style.
//
// If every result has a malformed title (no " - " separator between
// artist and release) we return ErrNoResults rather than ship garbage.
func (c *Client) Search(ctx context.Context, genre string) (SearchResult, error) {
	if genre == "" {
		if len(c.defaultGenres) == 0 {
			return SearchResult{}, fmt.Errorf("discogs: empty genre and no defaults")
		}
		genre = c.defaultGenres[c.rng.Intn(len(c.defaultGenres))]
	}

	paramKey := string(KindOf(genre)) // "genre" | "style"
	key := "search:" + paramKey + "=" + strings.ToLower(genre)
	params := url.Values{}
	params.Set("type", "release")
	params.Set(paramKey, genre)
	payload, err := c.fetchSearch(ctx, key, params)
	if err != nil {
		return SearchResult{}, err
	}
	return parsePickRandom(payload, c.rng)
}

// SearchQuery picks one result from a free-text query. Used by the
// user-initiated search path (StrategySearch). ROADMAP §v0.4 item 5:
// "Relies on Discogs' native fuzziness — no custom fuzzy matcher."
// Callers (queue.SearchHandler) normalize the query before calling.
//
// Unlike Search, we DON'T shuffle — Discogs already orders results by
// relevance, so the head of the list is the best match for the user's
// typed query. Shuffling would erase Discogs' ranking signal.
//
// Cache key incorporates the normalized query; identical queries share
// a cache row for up to 30 days.
func (c *Client) SearchQuery(ctx context.Context, query string) (SearchResult, error) {
	if query == "" {
		return SearchResult{}, ErrNoResults
	}
	key := "search:q=" + query
	params := url.Values{}
	params.Set("type", "release")
	params.Set("q", query)
	payload, err := c.fetchSearch(ctx, key, params)
	if err != nil {
		return SearchResult{}, err
	}
	return parsePickFirst(payload)
}

// fetchSearch returns the raw JSON body for a /database/search request
// described by params. Consults the cache (keyed by key) first; on miss
// it hits the API, stores the result, and returns it.
func (c *Client) fetchSearch(ctx context.Context, key string, params url.Values) ([]byte, error) {
	if c.cache != nil {
		if payload, err := c.cache.Get(ctx, key); err == nil {
			return payload, nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			c.log.Warn("discogs: cache read failed (falling through)", "err", err)
		}
	}

	if err := c.limiter.Wait(ctx); err != nil {
		return nil, err
	}

	u, _ := url.Parse(c.baseURL + "/database/search")
	if !params.Has("per_page") {
		params.Set("per_page", "100")
	}
	u.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("discogs: new request: %w", err)
	}
	req.Header.Set("Authorization", "Discogs token="+c.token)
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("discogs: http get: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// fallthrough
	case http.StatusTooManyRequests:
		return nil, ErrRateLimited
	default:
		return nil, fmt.Errorf("discogs: http %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("discogs: read body: %w", err)
	}

	if c.cache != nil {
		if err := c.cache.Put(ctx, key, body); err != nil {
			// Cache write is best-effort. Don't fail the search.
			c.log.Warn("discogs: cache write failed", "err", err)
		}
	}
	return body, nil
}

// buildResult turns one releaseResult into a well-formed SearchResult, or
// returns false if the title can't be split. Shared by all parse*
// variants so field population stays consistent.
func buildResult(r releaseResult) (SearchResult, bool) {
	artist, title, ok := splitArtistTitle(r.Title)
	if !ok {
		return SearchResult{}, false
	}
	return SearchResult{
		Title:         title,
		Artist:        artist,
		CatalogNumber: firstCatno(r.CatalogNumber),
		Year:          parseYear(r.Year),
		Thumb:         r.Thumb,
		ID:            r.ID,
	}, true
}

// parsePickRandom decodes a /database/search response and returns one
// random well-formed item. Used by the genre-based Search path.
func parsePickRandom(payload []byte, rng *rand.Rand) (SearchResult, error) {
	var resp searchResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return SearchResult{}, fmt.Errorf("discogs: unmarshal: %w", err)
	}
	if len(resp.Results) == 0 {
		return SearchResult{}, ErrNoResults
	}
	order := rng.Perm(len(resp.Results))
	for _, idx := range order {
		if res, ok := buildResult(resp.Results[idx]); ok {
			return res, nil
		}
	}
	return SearchResult{}, ErrNoResults
}

// parsePickFirst decodes a /database/search response and returns the
// first well-formed item (preserving Discogs' relevance ranking). Used
// by the auto-pick SearchQuery path (legacy; v0.4.1 PR C prefers Preview).
func parsePickFirst(payload []byte) (SearchResult, error) {
	var resp searchResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return SearchResult{}, fmt.Errorf("discogs: unmarshal: %w", err)
	}
	for _, r := range resp.Results {
		if res, ok := buildResult(r); ok {
			return res, nil
		}
	}
	return SearchResult{}, ErrNoResults
}

// parsePickAll decodes a /database/search response and returns up to
// `limit` well-formed items, preserving Discogs' relevance order.
//
// Dedup (v0.4.2 PR A): Discogs indexes the same release across every
// pressing — vinyl, CD, digital, regional catno variants — so a plain
// list for "Never Let Me Go" is dominated by five near-identical
// Placebo rows. We collapse on case-insensitive (artist, title) and
// keep the FIRST occurrence (highest relevance). The user sees the
// release once; the catno they get is whichever pressing Discogs
// ranks highest, and the download ladder will try it on its own merit.
//
// Malformed titles (no " - " separator) are skipped.
func parsePickAll(payload []byte, limit int) ([]SearchResult, error) {
	var resp searchResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return nil, fmt.Errorf("discogs: unmarshal: %w", err)
	}
	if limit < 1 {
		limit = 10
	}
	out := make([]SearchResult, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, r := range resp.Results {
		res, ok := buildResult(r)
		if !ok {
			continue
		}
		key := strings.ToLower(res.Artist) + "\x00" + strings.ToLower(res.Title)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, res)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// Preview returns up to `limit` candidates for a free-text user query.
// ROADMAP §v0.4 item 5's "user-initiated search" — same Discogs endpoint
// as SearchQuery, but the caller picks the winner instead of auto-
// selecting the head. Empty query → empty slice (not an error; the UI
// treats it as "hide dropdown").
//
// Cache key matches SearchQuery's so the two paths share the 30-day
// response cache.
func (c *Client) Preview(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	if limit < 1 {
		limit = 10
	}
	key := "search:q=" + query
	params := url.Values{}
	params.Set("type", "release")
	params.Set("q", query)
	payload, err := c.fetchSearch(ctx, key, params)
	if err != nil {
		return nil, err
	}
	return parsePickAll(payload, limit)
}

// Entity is the JSON shape for artist + label search hits. ID lets the
// frontend (v0.4.2 PR C) link to a detail view; Name is the display text;
// Thumb is an optional thumbnail URL from Discogs that the UI can render
// in the dropdown row.
type Entity struct {
	ID    int
	Name  string
	Thumb string
}

// SearchArtists returns up to `limit` artist hits for a free-text query.
// v0.4.2 PR B — the TopBar dropdown surfaces these alongside releases.
// Empty query → nil slice (treated as "hide section").
func (c *Client) SearchArtists(ctx context.Context, query string, limit int) ([]Entity, error) {
	return c.searchEntity(ctx, "artist", query, limit)
}

// SearchLabels returns up to `limit` label hits for a free-text query.
// Same semantics as SearchArtists; Discogs' label catalog is narrower
// than its artist catalog so expect fewer results per query.
func (c *Client) SearchLabels(ctx context.Context, query string, limit int) ([]Entity, error) {
	return c.searchEntity(ctx, "label", query, limit)
}

// searchEntity is the shared implementation for artist/label preview. The
// only difference is the type= param; response parsing uses the same
// entityResult shape. Cache key includes `type=` so artist and label
// queries for the same string don't collide with each other or with the
// release search's cache.
func (c *Client) searchEntity(ctx context.Context, entityType, query string, limit int) ([]Entity, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	if limit < 1 {
		limit = 10
	}
	key := "search:type=" + entityType + ":q=" + query
	params := url.Values{}
	params.Set("type", entityType)
	params.Set("q", query)
	payload, err := c.fetchSearch(ctx, key, params)
	if err != nil {
		return nil, err
	}
	return parseEntities(payload, limit)
}

// parseEntities decodes a /database/search response for type=artist or
// type=label. Dedupes by case-insensitive name (Discogs' "Daft Punk" and
// "Daft Punk (2)" are treated as distinct here — that's a real profile
// distinction in Discogs' data; we only collapse exact-name dupes).
func parseEntities(payload []byte, limit int) ([]Entity, error) {
	var resp struct {
		Results []entityResult `json:"results"`
	}
	if err := json.Unmarshal(payload, &resp); err != nil {
		return nil, fmt.Errorf("discogs: unmarshal entity: %w", err)
	}
	if limit < 1 {
		limit = 10
	}
	out := make([]Entity, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, r := range resp.Results {
		name := strings.TrimSpace(r.Title)
		if name == "" || r.ID == 0 {
			continue
		}
		key := strings.ToLower(name)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, Entity{
			ID:    r.ID,
			Name:  name,
			Thumb: r.Thumb,
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

type entityResult struct {
	ID    int    `json:"id"`
	Title string `json:"title"` // "Daft Punk" for artists, "Warp Records" for labels
	Thumb string `json:"thumb"`
}

// ---- v0.4.2 PR C — detail endpoints (artist/label/release) ---------------

// ReleaseDetail is the payload for a full release lookup — metadata +
// tracklist. Used by the /album/:id frontend view.
//
// v0.4.3: Thumb and Cover carry cover-art URLs at two sizes. Thumb
// is Discogs' listing thumbnail (~150 px, used in row views when
// we have a release id). Cover is the first primary image from the
// release's `images` array — typically ~600 px, used for the
// AlbumView hero. Either can be empty for releases without linked
// artwork; callers render a gradient placeholder in that case.
type ReleaseDetail struct {
	ID            int
	Title         string
	Artist        string
	Year          int
	CatalogNumber string
	Label         string
	Thumb         string
	Cover         string
	Tracks        []Track

	// v0.5: features used by similarity hydration. All optional —
	// missing fields stay zero/empty and similarity buckets that
	// need them bail out gracefully on zero IDs / empty slices.

	// ArtistID is the primary artist's Discogs ID (artists[0].id).
	// 0 when the release has no resolved primary artist (rare;
	// most releases credit at least one).
	ArtistID int

	// LabelID is the primary label's Discogs ID (labels[0].id).
	// 0 on white-label / not-on-label releases.
	LabelID int

	// Styles is the Discogs sub-genre vocabulary applied to the
	// release ("House", "Detroit Techno", "Vaporwave"). Often
	// 0-3 entries. Empty for under-tagged releases.
	Styles []string

	// Genres is the Discogs broad genre vocabulary ("Electronic",
	// "Rock"). Usually 1 entry. Empty rare.
	Genres []string

	// Collaborators are Discogs IDs of extraartists credited on
	// this release (vocalists, producers, remixers), excluding the
	// primary Artists[]. Used by the same-collaborators bucket in
	// PR C. Empty when the release has no extra credits.
	Collaborators []int
}

// Track is one row in a release's tracklist.
type Track struct {
	Position string // "A1", "B2", "1", ... — free-form per Discogs
	Title    string
	Duration string // "3:45" or "" when unknown
}

// EntityDetail is the payload for /artists/{id} or /labels/{id} —
// the entity's name plus a hero image. Used by ArtistView and
// LabelView to render the actual press photo / label logo Discogs
// has on file, instead of falling back to a member release's thumb.
//
// v0.4.4: introduced. Image is the URI of the first primary image
// from Discogs' images[]; empty when the entity has no linked
// artwork. Profile is left out — we only need the image for the
// hero, and adding profile would push us toward a richer
// "About this artist" view that's a separate scope.
type EntityDetail struct {
	ID    int
	Name  string
	Image string
}

// Artist fetches /artists/{id} for the entity's name + image. Cached
// alongside other Discogs responses under `artist:<id>` with the
// 30-day TTL. Returns ErrNoResults when Discogs returns 404 (the
// id is bogus or the artist was removed from the database).
func (c *Client) Artist(ctx context.Context, artistID int) (EntityDetail, error) {
	if artistID <= 0 {
		return EntityDetail{}, fmt.Errorf("discogs: artistID must be positive")
	}
	key := fmt.Sprintf("artist:%d", artistID)
	path := fmt.Sprintf("/artists/%d", artistID)
	payload, err := c.fetchRaw(ctx, key, path, nil)
	if err != nil {
		return EntityDetail{}, err
	}
	return parseEntityDetail(payload)
}

// Label is the label analog of Artist. Same response shape on the
// Discogs side (id, name, images[]).
func (c *Client) Label(ctx context.Context, labelID int) (EntityDetail, error) {
	if labelID <= 0 {
		return EntityDetail{}, fmt.Errorf("discogs: labelID must be positive")
	}
	key := fmt.Sprintf("label:%d", labelID)
	path := fmt.Sprintf("/labels/%d", labelID)
	payload, err := c.fetchRaw(ctx, key, path, nil)
	if err != nil {
		return EntityDetail{}, err
	}
	return parseEntityDetail(payload)
}

// parseEntityDetail decodes /artists/{id} or /labels/{id}. Both
// endpoints share the relevant fields (id, name, images[]).
// The first primary image wins; if no image is type=="primary",
// fall back to images[0]. Empty when no images are present.
func parseEntityDetail(payload []byte) (EntityDetail, error) {
	var resp struct {
		ID     int    `json:"id"`
		Name   string `json:"name"`
		Images []struct {
			Type string `json:"type"`
			URI  string `json:"uri"`
		} `json:"images"`
	}
	if err := json.Unmarshal(payload, &resp); err != nil {
		return EntityDetail{}, fmt.Errorf("discogs: unmarshal entity: %w", err)
	}
	image := ""
	for _, img := range resp.Images {
		if img.Type == "primary" && img.URI != "" {
			image = img.URI
			break
		}
	}
	if image == "" && len(resp.Images) > 0 {
		image = resp.Images[0].URI
	}
	return EntityDetail{
		ID:    resp.ID,
		Name:  strings.TrimSpace(resp.Name),
		Image: image,
	}, nil
}

// ArtistReleases returns up to `limit` releases credited to artistID,
// preserving Discogs' ordering (most-recent-first by default). Cached
// under `artist:<id>:releases` with the standard 30-day TTL.
//
// Discogs' /artists/{id}/releases returns both "Master" entries (an
// abstract "album release") and "Release" entries (specific pressings
// under the master). We keep only Release entries — masters lack the
// catno that the download ladder leans on.
func (c *Client) ArtistReleases(ctx context.Context, artistID, limit int) ([]SearchResult, error) {
	if artistID <= 0 {
		return nil, fmt.Errorf("discogs: artistID must be positive")
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	key := fmt.Sprintf("artist:%d:releases", artistID)
	path := fmt.Sprintf("/artists/%d/releases", artistID)
	params := url.Values{}
	params.Set("per_page", strconv.Itoa(limit))
	return c.fetchArtistOrLabelReleases(ctx, key, path, params, limit)
}

// LabelReleases returns up to `limit` releases issued on labelID.
// Cached under `label:<id>:releases`. Labels can have thousands of
// releases; the first-page cap is pragmatic, not a leak.
func (c *Client) LabelReleases(ctx context.Context, labelID, limit int) ([]SearchResult, error) {
	if labelID <= 0 {
		return nil, fmt.Errorf("discogs: labelID must be positive")
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	key := fmt.Sprintf("label:%d:releases", labelID)
	path := fmt.Sprintf("/labels/%d/releases", labelID)
	params := url.Values{}
	params.Set("per_page", strconv.Itoa(limit))
	return c.fetchArtistOrLabelReleases(ctx, key, path, params, limit)
}

// Release returns the full release metadata + tracklist for releaseID.
// Cached under `release:<id>`.
func (c *Client) Release(ctx context.Context, releaseID int) (ReleaseDetail, error) {
	if releaseID <= 0 {
		return ReleaseDetail{}, fmt.Errorf("discogs: releaseID must be positive")
	}
	key := fmt.Sprintf("release:%d", releaseID)
	path := fmt.Sprintf("/releases/%d", releaseID)
	payload, err := c.fetchRaw(ctx, key, path, nil)
	if err != nil {
		return ReleaseDetail{}, err
	}
	return parseReleaseDetail(payload)
}

// fetchArtistOrLabelReleases factors the shared pagination parse used
// by ArtistReleases and LabelReleases. Both endpoints respond with
// {pagination: {...}, releases: [...]} where each release carries an
// id, title, artist, year, catno, role, thumb.
//
// v0.4.4 update: masters are INCLUDED and treated as albums.
//
// Discogs' /artists/{id}/releases endpoint returns `format` as only
// the physical medium (`"12\""`, `"CD, Comp"`, etc.) — it does NOT
// include the "Album" / "EP" / "LP" classification tokens that
// /database/search emits. That meant our original "skip masters +
// parse format for Album" heuristic classified roughly zero
// entries as Album on real artists.
//
// Masters ARE exactly what users think of as albums ("Modal Soul",
// "Metaphorical Music"); each one has a `main_release` int pointing
// to a specific pressing that /releases/{id} will dereference into
// a tracklist. So we:
//   - keep masters, flag them IsAlbum=true, and rewrite their ID
//     to main_release so the AlbumView URL resolves
//   - also classify non-master releases with an Album/LP/EP
//     format token as albums (rarer on this endpoint, but they
//     show up and without this we'd miss them)
//   - prefer the master entry when a (artist,title) pair appears
//     in both master and release form; masters carry the album
//     concept and tend to have better thumbnails.
func (c *Client) fetchArtistOrLabelReleases(ctx context.Context, key, path string, params url.Values, limit int) ([]SearchResult, error) {
	payload, err := c.fetchRaw(ctx, key, path, params)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Releases []struct {
			ID          int    `json:"id"`
			Type        string `json:"type"`
			Title       string `json:"title"`
			Artist      string `json:"artist"`
			Year        int    `json:"year"`
			Catno       string `json:"catno"`
			Thumb       string `json:"thumb"`
			Format      string `json:"format"`
			MainRelease int    `json:"main_release"`
		} `json:"releases"`
	}
	if err := json.Unmarshal(payload, &resp); err != nil {
		return nil, fmt.Errorf("discogs: unmarshal releases: %w", err)
	}
	out := make([]SearchResult, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, r := range resp.Releases {
		title := strings.TrimSpace(r.Title)
		artist := strings.TrimSpace(r.Artist)
		if title == "" {
			continue
		}
		dedup := strings.ToLower(artist) + "\x00" + strings.ToLower(title)
		if _, dup := seen[dedup]; dup {
			continue
		}

		isMaster := r.Type == "master"
		id := r.ID
		if isMaster {
			// Routing target: main_release is the specific pressing
			// /releases/{id} can dereference. Fall back to the master id
			// (rare; Discogs virtually always sets main_release on
			// surfaced masters).
			if r.MainRelease > 0 {
				id = r.MainRelease
			}
		}
		if isMaster && id == 0 {
			// Nothing we can route to — skip rather than emit a broken row.
			continue
		}

		seen[dedup] = struct{}{}
		out = append(out, SearchResult{
			Title:         title,
			Artist:        artist,
			CatalogNumber: firstCatno(r.Catno),
			Year:          r.Year,
			Thumb:         r.Thumb,
			ID:            id,
			Format:        r.Format,
			IsAlbum:       isMaster || IsAlbumFormat(r.Format),
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// IsAlbumFormat reports whether a Discogs release `format` string
// describes a multi-track album. Discogs returns this as a comma-
// separated string of tokens like "CD, Album" or "12\", EP". We
// treat "Album", "LP", "EP", and "Mini-Album" as album; everything
// else (Single, Compilation without Album token, etc.) as single.
// Match is case-insensitive but boundary-aware via comma splitting
// so "EPK" doesn't false-positive on "EP". v0.4.4.
//
// Exported so internal/search can reuse the same classification.
// The artist/label releases endpoint rarely returns these tokens
// (it mostly returns physical medium only); masters carry the
// album concept on that endpoint. /database/search DOES return
// these tokens, which is where this function earns its keep.
func IsAlbumFormat(format string) bool {
	if format == "" {
		return false
	}
	for _, tok := range strings.Split(format, ",") {
		tok = strings.TrimSpace(strings.ToLower(tok))
		switch tok {
		case "album", "lp", "ep", "mini-album":
			return true
		}
	}
	return false
}

// fetchRaw is the low-level cached GET used by the detail endpoints.
// Parallels fetchSearch but for /artists, /labels, /releases paths
// rather than /database/search.
func (c *Client) fetchRaw(ctx context.Context, key, path string, params url.Values) ([]byte, error) {
	if c.cache != nil {
		if payload, err := c.cache.Get(ctx, key); err == nil {
			return payload, nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			c.log.Warn("discogs: cache read failed (falling through)", "err", err)
		}
	}
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, err
	}
	u, _ := url.Parse(c.baseURL + path)
	if params != nil {
		u.RawQuery = params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("discogs: new request: %w", err)
	}
	req.Header.Set("Authorization", "Discogs token="+c.token)
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("discogs: http get: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// fallthrough
	case http.StatusTooManyRequests:
		return nil, ErrRateLimited
	case http.StatusNotFound:
		return nil, ErrNoResults
	default:
		return nil, fmt.Errorf("discogs: http %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("discogs: read body: %w", err)
	}
	if c.cache != nil {
		if err := c.cache.Put(ctx, key, body); err != nil {
			c.log.Warn("discogs: cache write failed", "err", err)
		}
	}
	return body, nil
}

// parseReleaseDetail decodes a /releases/{id} response. Discogs
// returns the artist as an array (for collaborations) — we join the
// names with " & " to produce a human-readable string.
func parseReleaseDetail(payload []byte) (ReleaseDetail, error) {
	var resp struct {
		ID      int    `json:"id"`
		Title   string `json:"title"`
		Year    int    `json:"year"`
		Thumb   string `json:"thumb"`
		Artists []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"artists"`
		Labels []struct {
			ID    int    `json:"id"`
			Name  string `json:"name"`
			Catno string `json:"catno"`
		} `json:"labels"`
		Tracklist []struct {
			Position string `json:"position"`
			Title    string `json:"title"`
			Duration string `json:"duration"`
		} `json:"tracklist"`
		// v0.5: similarity hydration fields. styles[] are the
		// Discogs sub-genres ("House"); genres[] are the broad
		// vocabulary ("Electronic"). extraartists[].id are
		// non-primary credits we treat as collaborators.
		Styles       []string `json:"styles"`
		Genres       []string `json:"genres"`
		ExtraArtists []struct {
			ID int `json:"id"`
		} `json:"extraartists"`
		// Discogs release responses include an "images" array where
		// one entry has type=="primary" (front cover) and the rest
		// are type=="secondary" (back, inner, etc). Each has
		// resource_url (full size), uri (same), and uri150 (thumb).
		// We pick the primary for the hero; fall back to first image.
		Images []struct {
			Type        string `json:"type"`
			URI         string `json:"uri"`
			ResourceURL string `json:"resource_url"`
		} `json:"images"`
	}
	if err := json.Unmarshal(payload, &resp); err != nil {
		return ReleaseDetail{}, fmt.Errorf("discogs: unmarshal release: %w", err)
	}
	cover := ""
	for _, img := range resp.Images {
		if img.Type == "primary" && img.URI != "" {
			cover = img.URI
			break
		}
	}
	if cover == "" && len(resp.Images) > 0 {
		cover = resp.Images[0].URI
	}
	out := ReleaseDetail{
		ID:    resp.ID,
		Title: strings.TrimSpace(resp.Title),
		Year:  resp.Year,
		Thumb: resp.Thumb,
		Cover: cover,
	}
	names := make([]string, 0, len(resp.Artists))
	for _, a := range resp.Artists {
		if n := strings.TrimSpace(a.Name); n != "" {
			names = append(names, n)
		}
	}
	out.Artist = strings.Join(names, " & ")
	if len(resp.Artists) > 0 {
		out.ArtistID = resp.Artists[0].ID
	}
	if len(resp.Labels) > 0 {
		out.Label = strings.TrimSpace(resp.Labels[0].Name)
		out.CatalogNumber = firstCatno(resp.Labels[0].Catno)
		out.LabelID = resp.Labels[0].ID
	}
	out.Styles = resp.Styles
	out.Genres = resp.Genres
	if len(resp.ExtraArtists) > 0 {
		out.Collaborators = make([]int, 0, len(resp.ExtraArtists))
		seenCollab := make(map[int]struct{}, len(resp.ExtraArtists))
		for _, ea := range resp.ExtraArtists {
			if ea.ID <= 0 {
				continue
			}
			if _, dup := seenCollab[ea.ID]; dup {
				continue
			}
			// Don't list the primary artist as their own collaborator.
			if ea.ID == out.ArtistID {
				continue
			}
			seenCollab[ea.ID] = struct{}{}
			out.Collaborators = append(out.Collaborators, ea.ID)
		}
	}
	for _, t := range resp.Tracklist {
		title := strings.TrimSpace(t.Title)
		if title == "" {
			continue // Discogs sometimes has heading rows with empty titles
		}
		out.Tracks = append(out.Tracks, Track{
			Position: strings.TrimSpace(t.Position),
			Title:    title,
			Duration: strings.TrimSpace(t.Duration),
		})
	}
	return out, nil
}

// SweepCache is an optional maintenance hook: drops cache rows older than
// cacheTTL. Main.go calls it once at startup if Discogs is enabled.
func (c *Client) SweepCache(ctx context.Context) {
	if c.cache == nil {
		return
	}
	n, err := c.cache.Sweep(ctx)
	if err != nil {
		c.log.Warn("discogs: cache sweep failed", "err", err)
		return
	}
	if n > 0 {
		c.log.Info("discogs: cache swept", "rows", n)
	}
}

// ---- JSON shapes (we only extract what we need; rest is ignored by encoding/json) ----

type searchResponse struct {
	Results []releaseResult `json:"results"`
}

type releaseResult struct {
	// ID is the Discogs release ID. Populated on /database/search
	// responses (every result includes it). v0.5: hydration uses
	// this to follow up with /releases/{id} for the similarity
	// feature extraction.
	ID int `json:"id"`
	// Title is "Artist - Release" by Discogs convention.
	Title string `json:"title"`
	// CatalogNumber can be "" or "CAT-001, CAT-002" (comma-separated). We
	// grab the first as representative; Soulseek users tag with one catno
	// per file, not a list.
	CatalogNumber string `json:"catno"`
	// Year is a string in Discogs' JSON (occasionally "" or "0000" for
	// unknown). We parse to int and treat anything non-positive as unknown.
	Year string `json:"year"`
	// Thumb is Discogs' small cover-art URL. "" on releases without
	// linked artwork. v0.4.3.
	Thumb string `json:"thumb"`
}

// parseYear turns Discogs' year string into a non-negative int.
// "1972" -> 1972; "" / "0" / "unknown" -> 0.
func parseYear(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// splitArtistTitle turns "Artist Name - Release Title" into ("Artist Name",
// "Release Title", true). Returns (_, _, false) for malformed titles
// (no separator or empty halves).
//
// We intentionally only split on the first " - " so "DJ Shadow - Endtroducing.....
// Pre-Millennium Mix" stays intact as the title.
func splitArtistTitle(s string) (artist, title string, ok bool) {
	s = strings.TrimSpace(s)
	const sep = " - "
	i := strings.Index(s, sep)
	if i <= 0 {
		return "", "", false
	}
	artist = strings.TrimSpace(s[:i])
	title = strings.TrimSpace(s[i+len(sep):])
	if artist == "" || title == "" {
		return "", "", false
	}
	return artist, title, true
}

// firstCatno returns the first catalog number from a comma-separated list.
// Whitespace-trimmed. "" if input is empty or whitespace.
func firstCatno(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.Index(s, ","); i > 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

