package bandcamp_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/bandcamp"
	"github.com/macabc/muzika/internal/bus"
)

// fakeDiscover returns a handler that responds to POST /api/discover/1/discover_web
// with the given items. It also records the last-seen request body for assertions.
func fakeDiscover(t *testing.T, items []bandcamp.DiscoverItem, lastReqBody *bandcamp.DiscoverRequest) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/discover/1/discover_web" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if lastReqBody != nil {
			if err := json.Unmarshal(body, lastReqBody); err != nil {
				t.Errorf("server-side unmarshal: %v", err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(bandcamp.DiscoverResponse{Results: items})
	}
}

// deterministic rand source so tests don't flake on item selection.
func fixedRand() *rand.Rand { return rand.New(rand.NewSource(42)) }

func TestSearch_HonorsGenre(t *testing.T) {
	var reqBody bandcamp.DiscoverRequest
	srv := httptest.NewServer(fakeDiscover(t, []bandcamp.DiscoverItem{
		{Title: "Song A", BandName: "Artist A"},
	}, &reqBody))
	defer srv.Close()

	client := bandcamp.NewClient(srv.URL, []string{"electronic", "house"}, bandcamp.WithRand(fixedRand()))
	got, err := client.Search(context.Background(), "progressive-house")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got.Title != "Song A" || got.Artist != "Artist A" {
		t.Errorf("got %+v, want Song A / Artist A", got)
	}
	if len(reqBody.TagNormNames) != 1 || reqBody.TagNormNames[0] != "progressive-house" {
		t.Errorf("tag_norm_names sent to bandcamp was %v, want [progressive-house]", reqBody.TagNormNames)
	}
}

func TestSearch_EmptyGenreFallsBackToDefault(t *testing.T) {
	var reqBody bandcamp.DiscoverRequest
	srv := httptest.NewServer(fakeDiscover(t, []bandcamp.DiscoverItem{
		{Title: "X", BandName: "Y"},
	}, &reqBody))
	defer srv.Close()

	client := bandcamp.NewClient(srv.URL, []string{"jazz"}, bandcamp.WithRand(fixedRand()))
	if _, err := client.Search(context.Background(), ""); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(reqBody.TagNormNames) != 1 || reqBody.TagNormNames[0] != "jazz" {
		t.Errorf("tag_norm_names sent to bandcamp was %v, want [jazz]", reqBody.TagNormNames)
	}
}

func TestSearch_NoResults(t *testing.T) {
	srv := httptest.NewServer(fakeDiscover(t, []bandcamp.DiscoverItem{}, nil))
	defer srv.Close()

	client := bandcamp.NewClient(srv.URL, []string{"jazz"}, bandcamp.WithRand(fixedRand()))
	_, err := client.Search(context.Background(), "nonexistent")
	if err != bandcamp.ErrNoResults {
		t.Errorf("got %v, want ErrNoResults", err)
	}
}

func TestSearch_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := bandcamp.NewClient(srv.URL, []string{"jazz"}, bandcamp.WithRand(fixedRand()))
	_, err := client.Search(context.Background(), "x")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "http 500") {
		t.Errorf("error should mention http 500, got %v", err)
	}
}

// TestWorker_PublishesRequestDownload drives the full worker contract:
// DiscoveryIntent{StrategyRandom} → bandcamp search → RequestDownload with the same SongID.
func TestWorker_PublishesRequestDownload(t *testing.T) {
	srv := httptest.NewServer(fakeDiscover(t, []bandcamp.DiscoverItem{
		{Title: "Hit", BandName: "Band"},
	}, nil))
	defer srv.Close()

	client := bandcamp.NewClient(srv.URL, []string{"electronic"}, bandcamp.WithRand(fixedRand()))

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)
	svc := bandcamp.NewService(client, nil, b, nil)

	// Subscribe BEFORE triggering so we observe the publish.
	outCh := bus.Subscribe[bus.RequestDownload](b, "test/request-download")

	stubID := uuid.New()
	if err := svc.OnDiscoveryIntent(context.Background(), bus.DiscoveryIntent{
		SongID:   stubID,
		Strategy: bus.StrategyRandom,
		Genre:    "electronic",
	}); err != nil {
		t.Fatalf("handler: %v", err)
	}

	select {
	case ev := <-outCh:
		if ev.SongID != stubID {
			t.Errorf("SongID mismatch: got %v, want %v", ev.SongID, stubID)
		}
		if ev.Title != "Hit" || ev.Artist != "Band" {
			t.Errorf("metadata mismatch: %+v", ev)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for RequestDownload")
	}
}

// TestWorker_NoResultsIsNoop ensures a zero-result search doesn't publish.
func TestWorker_NoResultsIsNoop(t *testing.T) {
	srv := httptest.NewServer(fakeDiscover(t, []bandcamp.DiscoverItem{}, nil))
	defer srv.Close()

	client := bandcamp.NewClient(srv.URL, []string{"electronic"}, bandcamp.WithRand(fixedRand()))
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)
	svc := bandcamp.NewService(client, nil, b, nil)

	outCh := bus.Subscribe[bus.RequestDownload](b, "test/noop")

	err := svc.OnDiscoveryIntent(context.Background(), bus.DiscoveryIntent{
		SongID:   uuid.New(),
		Strategy: bus.StrategyRandom,
		Genre:    "electronic",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	select {
	case ev := <-outCh:
		t.Errorf("unexpected publish: %+v", ev)
	case <-time.After(150 * time.Millisecond):
		// expected — no publish
	}
}

// TestWorker_IgnoresNonRandomStrategy verifies the seeder's Strategy filter:
// a DiscoveryIntent with a Strategy bandcamp doesn't handle (StrategySearch
// here as the representative non-random case) must be silently dropped —
// another seeder (the Discogs worker in PR 2, the v0.5 similarity engine
// later) picks it up from the shared channel.
func TestWorker_IgnoresNonRandomStrategy(t *testing.T) {
	srv := httptest.NewServer(fakeDiscover(t, []bandcamp.DiscoverItem{
		{Title: "Hit", BandName: "Band"},
	}, nil))
	defer srv.Close()

	client := bandcamp.NewClient(srv.URL, []string{"electronic"}, bandcamp.WithRand(fixedRand()))
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)
	svc := bandcamp.NewService(client, nil, b, nil)

	outCh := bus.Subscribe[bus.RequestDownload](b, "test/ignored")

	if err := svc.OnDiscoveryIntent(context.Background(), bus.DiscoveryIntent{
		SongID:   uuid.New(),
		Strategy: bus.StrategySearch,
		Genre:    "electronic",
	}); err != nil {
		t.Fatalf("handler: %v", err)
	}

	select {
	case ev := <-outCh:
		t.Errorf("unexpected publish for non-random strategy: %+v", ev)
	case <-time.After(150 * time.Millisecond):
		// expected — no publish
	}
}
