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
}

// Search picks one random release from the top page of /database/search
// filtered by genre. Respects the rate limiter and the 30-day cache.
//
// Used by the passive-refill path (StrategyRandom). For user-initiated
// text search (StrategySearch, v0.4 PR 3), see SearchQuery.
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

	key := "search:genre=" + strings.ToLower(genre)
	params := url.Values{}
	params.Set("type", "release")
	params.Set("genre", genre)
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
	// Title is "Artist - Release" by Discogs convention.
	Title string `json:"title"`
	// CatalogNumber can be "" or "CAT-001, CAT-002" (comma-separated). We
	// grab the first as representative; Soulseek users tag with one catno
	// per file, not a list.
	CatalogNumber string `json:"catno"`
	// Year is a string in Discogs' JSON (occasionally "" or "0000" for
	// unknown). We parse to int and treat anything non-positive as unknown.
	Year string `json:"year"`
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

