package discogs_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/bus"
	"github.com/macabc/muzika/internal/discogs"
)

// fixedRand makes Search deterministic in tests.
func fixedRand() *rand.Rand { return rand.New(rand.NewSource(42)) }

// fakeResults returns a handler that responds to /database/search with the
// given results. It records every request URL for assertions.
func fakeResults(t *testing.T, items []map[string]any, got *[]string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/database/search" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		if got != nil {
			*got = append(*got, r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"results": items})
	}
}

// newTestClient builds a Client pointed at srv with a non-blocking limiter
// (5 burst, 100/s — effectively instant for tests).
func newTestClient(srv *httptest.Server) *discogs.Client {
	return discogs.NewClient(srv.URL, "test-pat", []string{"Electronic"},
		discogs.WithRand(fixedRand()),
		discogs.WithLimiter(100, 100),
	)
}

func TestSearch_HonorsGenre(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(fakeResults(t, []map[string]any{
		{"title": "Shanti People - Saraswati", "catno": "PAR-001"},
	}, &queries))
	defer srv.Close()

	c := newTestClient(srv)
	res, err := c.Search(context.Background(), "Electronic")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Artist != "Shanti People" || res.Title != "Saraswati" {
		t.Errorf("got %+v, want Shanti People / Saraswati", res)
	}
	if res.CatalogNumber != "PAR-001" {
		t.Errorf("catno=%q, want PAR-001", res.CatalogNumber)
	}
	if len(queries) != 1 || !strings.Contains(queries[0], "genre=Electronic") {
		t.Errorf("genre not in query: %v", queries)
	}
	if len(queries) != 1 || !strings.Contains(queries[0], "type=release") {
		t.Errorf("type=release not in query: %v", queries)
	}
}

func TestSearch_EmptyGenreFallsBackToDefault(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(fakeResults(t, []map[string]any{
		{"title": "A - B", "catno": ""},
	}, &queries))
	defer srv.Close()

	c := newTestClient(srv)
	if _, err := c.Search(context.Background(), ""); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(queries) != 1 || !strings.Contains(queries[0], "genre=Electronic") {
		t.Errorf("default genre not honored: %v", queries)
	}
}

func TestSearch_NoResults(t *testing.T) {
	srv := httptest.NewServer(fakeResults(t, []map[string]any{}, nil))
	defer srv.Close()
	c := newTestClient(srv)
	if _, err := c.Search(context.Background(), "Electronic"); err != discogs.ErrNoResults {
		t.Errorf("got %v, want ErrNoResults", err)
	}
}

func TestSearch_MalformedTitlesSkipped(t *testing.T) {
	// First result has no " - " separator; second is well-formed. The client
	// should skip the first and return the second.
	srv := httptest.NewServer(fakeResults(t, []map[string]any{
		{"title": "NoSeparatorHere", "catno": "X-1"},
		{"title": "Artist B - Title B", "catno": "X-2"},
	}, nil))
	defer srv.Close()

	// Use a rand seed that visits the malformed one first. With Perm(2) and
	// seed 42 the order is deterministic; verify by asserting which one wins.
	c := newTestClient(srv)
	res, err := c.Search(context.Background(), "Electronic")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Artist == "" || res.Title == "" {
		t.Errorf("empty result: %+v", res)
	}
	// Must NOT be the malformed one.
	if res.Artist == "NoSeparatorHere" {
		t.Errorf("picked malformed title: %+v", res)
	}
}

func TestSearch_AllMalformedReturnsErrNoResults(t *testing.T) {
	srv := httptest.NewServer(fakeResults(t, []map[string]any{
		{"title": "OneWord", "catno": "X"},
		{"title": "AnotherWord", "catno": "Y"},
	}, nil))
	defer srv.Close()
	c := newTestClient(srv)
	if _, err := c.Search(context.Background(), "Electronic"); err != discogs.ErrNoResults {
		t.Errorf("got %v, want ErrNoResults", err)
	}
}

func TestSearch_FirstCatnoOnly(t *testing.T) {
	srv := httptest.NewServer(fakeResults(t, []map[string]any{
		{"title": "Artist - Title", "catno": "CAT-001, CAT-002"},
	}, nil))
	defer srv.Close()
	c := newTestClient(srv)
	res, err := c.Search(context.Background(), "Electronic")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.CatalogNumber != "CAT-001" {
		t.Errorf("got catno %q, want CAT-001 (first only)", res.CatalogNumber)
	}
}

func TestSearch_RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c := newTestClient(srv)
	if _, err := c.Search(context.Background(), "Electronic"); err != discogs.ErrRateLimited {
		t.Errorf("got %v, want ErrRateLimited", err)
	}
}

// TestSearch_CacheHit: first call goes to network; second call for the same
// genre hits the cache and doesn't touch the network.
func TestSearch_CacheHit(t *testing.T) {
	var hits atomic.Int64
	items := []map[string]any{{"title": "Artist - Title", "catno": "X-1"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"results": items})
	}))
	defer srv.Close()

	db := newTempDB(t)
	c := discogs.NewClient(srv.URL, "tok", []string{"Electronic"},
		discogs.WithRand(fixedRand()),
		discogs.WithLimiter(100, 100),
		discogs.WithCache(db),
	)

	if _, err := c.Search(context.Background(), "Electronic"); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := c.Search(context.Background(), "Electronic"); err != nil {
		t.Fatalf("second: %v", err)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("network hits = %d, want 1 (cache missed)", got)
	}
}

// TestWorker_PublishesRequestDownloadWithCatno is the end-to-end contract:
// a DiscoveryIntent lands, the worker asks Discogs, and a RequestDownload
// pops out with CatalogNumber filled.
func TestWorker_PublishesRequestDownloadWithCatno(t *testing.T) {
	srv := httptest.NewServer(fakeResults(t, []map[string]any{
		{"title": "Boards Of Canada - Music Has The Right To Children", "catno": "WARP-55"},
	}, nil))
	defer srv.Close()

	c := newTestClient(srv)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)
	svc := discogs.NewService(c, nil, b, nil, nil)

	outCh := bus.Subscribe[bus.RequestDownload](b, "test/req-download")

	stubID := uuid.New()
	if err := svc.OnDiscoveryIntent(context.Background(), bus.DiscoveryIntent{
		SongID:   stubID,
		Strategy: bus.StrategyRandom,
		Genre:    "Electronic",
	}); err != nil {
		t.Fatalf("handler: %v", err)
	}

	select {
	case ev := <-outCh:
		if ev.SongID != stubID {
			t.Errorf("SongID mismatch: %v vs %v", ev.SongID, stubID)
		}
		if ev.Artist != "Boards Of Canada" || ev.Title != "Music Has The Right To Children" {
			t.Errorf("metadata: %+v", ev)
		}
		if ev.CatalogNumber != "WARP-55" {
			t.Errorf("catno=%q, want WARP-55", ev.CatalogNumber)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for RequestDownload")
	}
}

// TestWorker_IgnoresOtherStrategies mirrors the bandcamp filter test: any
// Strategy other than StrategyRandom is silently dropped (another subscriber
// will handle it).
func TestWorker_IgnoresOtherStrategies(t *testing.T) {
	srv := httptest.NewServer(fakeResults(t, []map[string]any{
		{"title": "A - B", "catno": "X"},
	}, nil))
	defer srv.Close()

	c := newTestClient(srv)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)
	svc := discogs.NewService(c, nil, b, nil, nil)

	outCh := bus.Subscribe[bus.RequestDownload](b, "test/non-random")

	for _, strat := range []bus.Strategy{
		bus.StrategyGenre, bus.StrategySearch, bus.StrategySimilarSong, bus.StrategySimilarPlaylist,
	} {
		if err := svc.OnDiscoveryIntent(context.Background(), bus.DiscoveryIntent{
			SongID: uuid.New(), Strategy: strat,
		}); err != nil {
			t.Fatalf("handler (%s): %v", strat, err)
		}
	}
	select {
	case ev := <-outCh:
		t.Errorf("unexpected publish: %+v", ev)
	case <-time.After(120 * time.Millisecond):
		// expected
	}
}

// TestSearchQuery_PreviewDedupesByArtistTitle: v0.4.2 PR A. Discogs
// surfaces the same release under multiple pressings (vinyl, CD,
// regional variants — different catnos, same (artist, title)). The
// dropdown should show each release once. Tested through the Preview
// path since that's where the dropdown is populated.
func TestSearchQuery_PreviewDedupesByArtistTitle(t *testing.T) {
	srv := httptest.NewServer(fakeResults(t, []map[string]any{
		// Three pressings of the same Placebo release, different catnos.
		{"title": "Placebo - Never Let Me Go", "catno": "SOAKLP263", "year": "2022"},
		{"title": "Placebo - Never Let Me Go", "catno": "SOAKLPR263", "year": "2022"},
		{"title": "Placebo - Never Let Me Go", "catno": "SOAK263", "year": "2022"},
		// Different release.
		{"title": "Florence + The Machine - Never Let Me Go", "catno": "VF044", "year": "2012"},
		// Case variant — must also collapse into the Placebo row.
		{"title": "placebo - never let me go", "catno": "SOAKLPV263", "year": "2022"},
	}, nil))
	defer srv.Close()

	c := newTestClient(srv)
	res, err := c.Preview(context.Background(), "never let me go", 10)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("expected 2 after dedup, got %d: %+v", len(res), res)
	}
	// First occurrence of each (artist, title) wins — so the Placebo
	// candidate should carry the FIRST catno (SOAKLP263), not any of the
	// later ones we discarded.
	if res[0].Artist != "Placebo" || res[0].CatalogNumber != "SOAKLP263" {
		t.Errorf("first entry wrong: %+v", res[0])
	}
	if res[1].Artist != "Florence + The Machine" {
		t.Errorf("second entry wrong: %+v", res[1])
	}
}

// TestSearchQuery_HonorsQueryParam: SearchQuery hits /database/search with
// q=<query> and type=release, and the result's Title/Artist come from the
// first well-formed item (no shuffle — Discogs' ranking wins).
func TestSearchQuery_HonorsQueryParam(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(fakeResults(t, []map[string]any{
		{"title": "Best Match - Winner", "catno": "A-1"},
		{"title": "Lower Rank - Loser", "catno": "A-2"},
	}, &queries))
	defer srv.Close()

	c := newTestClient(srv)
	res, err := c.SearchQuery(context.Background(), "boards of canada")
	if err != nil {
		t.Fatalf("SearchQuery: %v", err)
	}
	if res.Artist != "Best Match" || res.Title != "Winner" {
		t.Errorf("first-result ranking not honored: %+v", res)
	}
	if len(queries) != 1 || !strings.Contains(queries[0], "q=boards+of+canada") {
		t.Errorf("q= not in query: %v", queries)
	}
	if !strings.Contains(queries[0], "type=release") {
		t.Errorf("type=release missing: %v", queries)
	}
}

// TestSearchQuery_EmptyIsErrNoResults
func TestSearchQuery_EmptyIsErrNoResults(t *testing.T) {
	srv := httptest.NewServer(fakeResults(t, []map[string]any{}, nil))
	defer srv.Close()
	c := newTestClient(srv)
	if _, err := c.SearchQuery(context.Background(), ""); err != discogs.ErrNoResults {
		t.Errorf("got %v, want ErrNoResults", err)
	}
}

// TestSearchQuery_CachesByQuery: two calls for the same query hit the
// network once; two different queries hit twice.
func TestSearchQuery_CachesByQuery(t *testing.T) {
	var hits atomic.Int64
	items := []map[string]any{{"title": "Artist - Title", "catno": "X-1"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"results": items})
	}))
	defer srv.Close()

	db := newTempDB(t)
	c := discogs.NewClient(srv.URL, "tok", []string{"Electronic"},
		discogs.WithRand(fixedRand()),
		discogs.WithLimiter(100, 100),
		discogs.WithCache(db),
	)

	_, _ = c.SearchQuery(context.Background(), "alpha")
	_, _ = c.SearchQuery(context.Background(), "alpha") // cache hit
	_, _ = c.SearchQuery(context.Background(), "beta")  // cache miss

	if got := hits.Load(); got != 2 {
		t.Errorf("network hits = %d, want 2 (alpha cached, beta fresh)", got)
	}
}

// TestWorker_SearchStrategyRoutesThroughSearchQuery: a DiscoveryIntent with
// Strategy=StrategySearch publishes a RequestDownload with Strategy also
// set to StrategySearch so the download worker can do origin-aware relax.
func TestWorker_SearchStrategyRoutesThroughSearchQuery(t *testing.T) {
	srv := httptest.NewServer(fakeResults(t, []map[string]any{
		{"title": "Björk - Debut", "catno": "ONE-001"},
	}, nil))
	defer srv.Close()

	c := newTestClient(srv)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)
	svc := discogs.NewService(c, nil, b, nil, nil)

	outCh := bus.Subscribe[bus.RequestDownload](b, "test/search-path")

	stubID := uuid.New()
	if err := svc.OnDiscoveryIntent(context.Background(), bus.DiscoveryIntent{
		SongID:   stubID,
		Strategy: bus.StrategySearch,
		Query:    "björk debut",
	}); err != nil {
		t.Fatalf("handler: %v", err)
	}

	select {
	case ev := <-outCh:
		if ev.Strategy != bus.StrategySearch {
			t.Errorf("event Strategy = %q, want %q", ev.Strategy, bus.StrategySearch)
		}
		if ev.Artist != "Björk" || ev.Title != "Debut" {
			t.Errorf("metadata: %+v", ev)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for RequestDownload")
	}
}

// TestWorker_RespectsPreferredSources: an intent that names "bandcamp" in
// PreferredSources is dropped by Discogs.
func TestWorker_RespectsPreferredSources(t *testing.T) {
	srv := httptest.NewServer(fakeResults(t, []map[string]any{
		{"title": "A - B", "catno": "X"},
	}, nil))
	defer srv.Close()

	c := newTestClient(srv)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)
	svc := discogs.NewService(c, nil, b, nil, nil)

	outCh := bus.Subscribe[bus.RequestDownload](b, "test/preferred-sources")

	err := svc.OnDiscoveryIntent(context.Background(), bus.DiscoveryIntent{
		SongID:           uuid.New(),
		Strategy:         bus.StrategyRandom,
		PreferredSources: []string{"bandcamp"},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	select {
	case ev := <-outCh:
		t.Errorf("unexpected publish despite PreferredSources=[bandcamp]: %+v", ev)
	case <-time.After(120 * time.Millisecond):
		// expected
	}
}
