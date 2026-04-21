// Package bandcamp scrapes Bandcamp's discover endpoint to populate the song
// catalog when the queue runs low. Consumes DiscoveryIntent (StrategyRandom
// only, see worker.go), produces RequestDownload. Genre is honored (replaces
// the old hardcoded "hisa").
package bandcamp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"time"
)

// ErrNoResults is returned when the discover endpoint yields zero items.
var ErrNoResults = errors.New("bandcamp: no results")

// Default base URL for production.
const defaultBaseURL = "https://bandcamp.com"

// Client wraps the HTTP traffic to bandcamp.com.
type Client struct {
	baseURL     string
	defaultTags []string
	httpClient  *http.Client
	rng         *rand.Rand
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient overrides the default http.Client (useful in tests).
func WithHTTPClient(c *http.Client) Option {
	return func(b *Client) { b.httpClient = c }
}

// WithRand overrides the random source (useful in tests).
func WithRand(r *rand.Rand) Option {
	return func(b *Client) { b.rng = r }
}

// NewClient constructs a Client. If baseURL is empty, bandcamp.com is used.
// defaultTags is the fallback when an incoming DiscoveryIntent.Genre is empty.
func NewClient(baseURL string, defaultTags []string, opts ...Option) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	c := &Client{
		baseURL:     baseURL,
		defaultTags: defaultTags,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		//nolint:gosec // G404: non-crypto random is fine for picking a result
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// DiscoverRequest is the POST body for /api/discover/1/discover_web.
//
// As of 2026 Bandcamp's discover page (DiscoverApp.vue_en bundle) ships
// these exact params. The endpoint rejects the older tag/slice/page shape
// with {"error_type":"Endpoints::MissingParamError"}, so every field here
// is mandatory even if null. See makeParams() in the Vite bundle for
// upstream definition.
type DiscoverRequest struct {
	CategoryID         int      `json:"category_id"`
	TagNormNames       []string `json:"tag_norm_names"`
	GeonameID          int      `json:"geoname_id"`
	Slice              string   `json:"slice"`
	TimeFacetID        *int     `json:"time_facet_id"`
	Cursor             *string  `json:"cursor"`
	Size               int      `json:"size"`
	IncludeResultTypes []string `json:"include_result_types"`
}

// DiscoverResponse is the JSON shape returned by /api/discover/1/discover_web.
// We only extract the fields we need (title + band name). Unknown fields are
// tolerated because encoding/json ignores them by default.
type DiscoverResponse struct {
	Results []DiscoverItem `json:"results"`
}

// DiscoverItem is a single discover-API hit.
type DiscoverItem struct {
	Title    string `json:"title"`
	BandName string `json:"band_name"`
}

// discover POSTs to /api/discover/1/discover_web with the given tag and
// returns the parsed response.
func (c *Client) discover(ctx context.Context, tag string) (DiscoverResponse, error) {
	body, err := json.Marshal(DiscoverRequest{
		CategoryID:         0,             // "all categories"
		TagNormNames:       []string{tag}, // single-tag search
		GeonameID:          0,             // "all locations"
		Slice:              "top",         // most popular this week
		TimeFacetID:        nil,           // no time filter
		Cursor:             nil,           // first page
		Size:               60,            // maxBatchSize in bundle
		IncludeResultTypes: []string{"a"}, // albums only (s = songs, we want albums so Soulseek finds the release)
	})
	if err != nil {
		return DiscoverResponse{}, fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/api/discover/1/discover_web", bytes.NewReader(body))
	if err != nil {
		return DiscoverResponse{}, fmt.Errorf("new request: %w", err)
	}
	// Mimic a browser to avoid casual bot filtering — same approach as the
	// old Java jsoup client.
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (X11; Linux aarch64; rv:128.0) Gecko/20100101 Firefox/128.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return DiscoverResponse{}, fmt.Errorf("http post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return DiscoverResponse{}, fmt.Errorf("discover: http %d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return DiscoverResponse{}, fmt.Errorf("read body: %w", err)
	}
	var out DiscoverResponse
	if err := json.Unmarshal(b, &out); err != nil {
		return DiscoverResponse{}, fmt.Errorf("unmarshal: %w", err)
	}
	return out, nil
}
