package search_test

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/macabc/muzika/internal/discogs"
	"github.com/macabc/muzika/internal/search"
)

// TestPreview_EmptyQueryReturnsEmpty: typeahead UX — an empty input
// means "hide the dropdown"; the preview should not make any HTTP call
// and should return all-empty (but non-nil) sections so the frontend
// can render unchanged.
func TestPreview_EmptyQueryReturnsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("no HTTP call expected for empty query")
	}))
	defer srv.Close()

	c := discogs.NewClient(srv.URL, "tok", nil,
		discogs.WithRand(rand.New(rand.NewSource(1))),
		discogs.WithLimiter(100, 100),
	)
	p := search.NewPreviewer(c)

	res, err := p.Preview(context.Background(), "   ")
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if res.Genres == nil || res.Artists == nil || res.Releases == nil || res.Labels == nil {
		t.Errorf("all sections must be non-nil (empty arrays), got %+v", res)
	}
	if len(res.Genres) != 0 || len(res.Artists) != 0 || len(res.Releases) != 0 || len(res.Labels) != 0 {
		t.Errorf("expected all sections empty, got %+v", res)
	}
}

// TestPreview_MultiCategory: v0.4.2 PR B. One query hits Discogs' type=
// release, type=artist, and type=label in parallel and returns a
// Preview with all four sections populated (plus genre suggestions
// matched client-side against the fixed vocabulary).
//
// The fake Discogs server inspects the `type=` param to decide which
// fixture to serve.
func TestPreview_MultiCategory(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/database/search" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("type") {
		case "release":
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{
				{"title": "Daft Punk - Discovery", "catno": "WPCR-80083", "year": "2001"},
			}})
		case "artist":
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{
				{"id": 1289, "title": "Daft Punk", "thumb": ""},
			}})
		case "label":
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{
				{"id": 34, "title": "Virgin Records", "thumb": ""},
			}})
		default:
			t.Errorf("unexpected type param: %q", r.URL.Query().Get("type"))
		}
	}))
	defer srv.Close()

	c := discogs.NewClient(srv.URL, "tok", nil,
		discogs.WithRand(rand.New(rand.NewSource(1))),
		discogs.WithLimiter(100, 100),
	)
	p := search.NewPreviewer(c)

	res, err := p.Preview(context.Background(), "daft punk")
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}

	if len(res.Releases) != 1 || res.Releases[0].Title != "Discovery" {
		t.Errorf("releases wrong: %+v", res.Releases)
	}
	if len(res.Artists) != 1 || res.Artists[0].Name != "Daft Punk" || res.Artists[0].ID != 1289 {
		t.Errorf("artists wrong: %+v", res.Artists)
	}
	if len(res.Labels) != 1 || res.Labels[0].Name != "Virgin Records" {
		t.Errorf("labels wrong: %+v", res.Labels)
	}
	// "daft punk" substring doesn't match any genre name.
	if len(res.Genres) != 0 {
		t.Errorf("genres should be empty for 'daft punk' query, got %v", res.Genres)
	}
}

// TestPreview_GenreSuggestion: typing "elec" surfaces "Electronic"
// from the closed Discogs vocabulary (matched client-side, no HTTP).
func TestPreview_GenreSuggestion(t *testing.T) {
	// Only serves empty results — genre matching is client-side.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{}})
	}))
	defer srv.Close()

	c := discogs.NewClient(srv.URL, "tok", nil,
		discogs.WithRand(rand.New(rand.NewSource(1))),
		discogs.WithLimiter(100, 100),
	)
	p := search.NewPreviewer(c)

	res, err := p.Preview(context.Background(), "elec")
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	found := false
	for _, g := range res.Genres {
		if strings.EqualFold(g, "Electronic") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'Electronic' among genres, got %v", res.Genres)
	}
}

// TestPreview_PartialFailureDegradesGracefully: if Discogs returns an
// error for one type (say, labels) but the others return OK, the
// dropdown should still populate with what succeeded rather than
// failing the whole request. Only an all-three-failed case bubbles up.
func TestPreview_PartialFailureDegradesGracefully(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("type") == "label" {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("type") {
		case "release":
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{
				{"title": "A - B", "catno": "X", "year": "2020"},
			}})
		case "artist":
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{
				{"id": 7, "title": "An Artist"},
			}})
		}
	}))
	defer srv.Close()

	c := discogs.NewClient(srv.URL, "tok", nil,
		discogs.WithRand(rand.New(rand.NewSource(1))),
		discogs.WithLimiter(100, 100),
	)
	p := search.NewPreviewer(c)

	res, err := p.Preview(context.Background(), "anything")
	if err != nil {
		t.Fatalf("expected partial success, got err: %v", err)
	}
	if len(res.Releases) != 1 || len(res.Artists) != 1 {
		t.Errorf("successful sections must still populate: %+v", res)
	}
	if len(res.Labels) != 0 {
		t.Errorf("failed section must be empty, got %+v", res.Labels)
	}
}

// TestPreview_NilClientReturnsErrDiscogsDisabled: when Discogs is off
// main.go constructs the Previewer with a nil client; preview must
// fail with a distinct error so the handler can emit 503.
func TestPreview_NilClientReturnsErrDiscogsDisabled(t *testing.T) {
	p := search.NewPreviewer(nil)

	_, err := p.Preview(context.Background(), "aa")
	if !errors.Is(err, search.ErrDiscogsDisabled) {
		t.Errorf("got %v, want ErrDiscogsDisabled", err)
	}
}
