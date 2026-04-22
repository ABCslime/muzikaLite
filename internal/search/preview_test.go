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

// TestArtist_ReturnsReleases: v0.4.2 PR C. Artist detail fetches
// /artists/{id}/releases and returns a Candidate slice the frontend
// can feed back into searchAcquire.
//
// v0.4.4 update: masters are NO LONGER filtered out — they carry the
// album concept on the /artists/{id}/releases endpoint (which
// doesn't return "Album" format tokens on non-master releases).
// Masters pass through with IsAlbum=true and ID=main_release, so
// /album/{id} resolves to a specific pressing's tracklist. A
// master deduplicates a same-title release that follows it.
func TestArtist_ReturnsReleases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/artists/1289/releases" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"releases": []map[string]any{
				// Master — main_release gives the routing target.
				{"id": 1, "type": "master", "main_release": 42, "title": "Discovery", "artist": "Daft Punk", "year": 2001, "catno": ""},
				// Same-title release — deduped by the master above.
				{"id": 2, "type": "release", "title": "Discovery", "artist": "Daft Punk", "year": 2001, "catno": "WPCR-80083"},
				// Different title, passes through as a single (no format token).
				{"id": 3, "type": "release", "title": "Homework", "artist": "Daft Punk", "year": 1997, "catno": "VUS45"},
			},
		})
	}))
	defer srv.Close()

	c := discogs.NewClient(srv.URL, "tok", nil,
		discogs.WithRand(rand.New(rand.NewSource(1))),
		discogs.WithLimiter(100, 100),
	)
	p := search.NewPreviewer(c)

	detail, err := p.Artist(context.Background(), 1289)
	if err != nil {
		t.Fatalf("Artist: %v", err)
	}
	if detail.ID != 1289 {
		t.Errorf("ID = %d, want 1289", detail.ID)
	}
	if detail.Name != "Daft Punk" {
		t.Errorf("Name = %q, want 'Daft Punk' (inherited from first release)", detail.Name)
	}
	if len(detail.Releases) != 2 {
		t.Fatalf("Releases count = %d, want 2 (master + non-dedup release)", len(detail.Releases))
	}
	// First entry: the master, with ID rewritten to main_release and IsAlbum=true.
	r0 := detail.Releases[0]
	if r0.Title != "Discovery" || r0.ID != 42 || !r0.IsAlbum {
		t.Errorf("first release wrong: %+v (want Title=Discovery ID=42 IsAlbum=true)", r0)
	}
	// Second entry: the single, NOT flagged as album.
	r1 := detail.Releases[1]
	if r1.Title != "Homework" || r1.IsAlbum {
		t.Errorf("second release wrong: %+v (want Title=Homework IsAlbum=false)", r1)
	}
}

// TestLabel_ReturnsReleases: same as Artist path but for labels.
func TestLabel_ReturnsReleases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/labels/23528/releases" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"releases": []map[string]any{
				{"id": 10, "type": "release", "title": "Selected Ambient Works", "artist": "Aphex Twin", "year": 1994, "catno": "WARP LP 39"},
				{"id": 11, "type": "release", "title": "Music Has The Right To Children", "artist": "Boards Of Canada", "year": 1998, "catno": "WARPCD55"},
			},
		})
	}))
	defer srv.Close()

	c := discogs.NewClient(srv.URL, "tok", nil,
		discogs.WithRand(rand.New(rand.NewSource(1))),
		discogs.WithLimiter(100, 100),
	)
	p := search.NewPreviewer(c)

	detail, err := p.Label(context.Background(), 23528)
	if err != nil {
		t.Fatalf("Label: %v", err)
	}
	if len(detail.Releases) != 2 {
		t.Fatalf("Releases count = %d, want 2", len(detail.Releases))
	}
	if detail.Releases[0].Artist != "Aphex Twin" {
		t.Errorf("first release artist = %q", detail.Releases[0].Artist)
	}
}

// TestRelease_ReturnsTracklist: /releases/{id} returns full detail
// including tracklist. Artists array joins with " & ". Empty
// tracklist rows (heading separators Discogs sometimes returns)
// must be dropped.
func TestRelease_ReturnsTracklist(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/releases/55" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":    55,
			"title": "Discovery",
			"year":  2001,
			"artists": []map[string]any{
				{"name": "Daft Punk"},
			},
			"labels": []map[string]any{
				{"name": "Virgin", "catno": "WPCR-80083"},
			},
			"tracklist": []map[string]any{
				{"position": "1", "title": "One More Time", "duration": "5:20"},
				{"position": "2", "title": "Aerodynamic", "duration": "3:27"},
				// Heading row — empty title, must be dropped.
				{"position": "", "title": "", "duration": ""},
				{"position": "3", "title": "Digital Love", "duration": "4:58"},
			},
		})
	}))
	defer srv.Close()

	c := discogs.NewClient(srv.URL, "tok", nil,
		discogs.WithRand(rand.New(rand.NewSource(1))),
		discogs.WithLimiter(100, 100),
	)
	p := search.NewPreviewer(c)

	detail, err := p.Release(context.Background(), 55)
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if detail.Title != "Discovery" || detail.Artist != "Daft Punk" {
		t.Errorf("metadata wrong: %+v", detail)
	}
	if detail.CatalogNumber != "WPCR-80083" {
		t.Errorf("catno = %q", detail.CatalogNumber)
	}
	if detail.Label != "Virgin" {
		t.Errorf("label = %q", detail.Label)
	}
	if len(detail.Tracks) != 3 {
		t.Fatalf("tracks = %d, want 3 (empty-title row dropped)", len(detail.Tracks))
	}
	if detail.Tracks[0].Title != "One More Time" || detail.Tracks[0].Duration != "5:20" {
		t.Errorf("first track wrong: %+v", detail.Tracks[0])
	}
}

// TestArtist_NilClientReturnsErrDiscogsDisabled: Discogs off -> 503
// via the shared ErrDiscogsDisabled from the preview path.
func TestArtist_NilClientReturnsErrDiscogsDisabled(t *testing.T) {
	p := search.NewPreviewer(nil)
	_, err := p.Artist(context.Background(), 1)
	if !errors.Is(err, search.ErrDiscogsDisabled) {
		t.Errorf("got %v, want ErrDiscogsDisabled", err)
	}
}
