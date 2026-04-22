package discogs_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/macabc/muzika/internal/discogs"
	"github.com/macabc/muzika/internal/similarity"
	discogsbuckets "github.com/macabc/muzika/internal/similarity/buckets/discogs"
)

// TestSameArtist_BailsWithoutArtistID asserts the bucket
// returns nothing (rather than panicking) when the seed didn't
// resolve to a Discogs artist. Bandcamp-only seeds hit this path;
// the engine then tries other buckets and may still produce a
// pick or surface ErrSeedUnknown if all miss.
func TestSameArtist_BailsWithoutArtistID(t *testing.T) {
	b := discogsbuckets.NewSameArtist(testClient(t, nil))
	got, err := b.Candidates(context.Background(), similarity.Seed{
		Title: "X", Artist: "Y", DiscogsArtistID: 0,
	})
	if err != nil || len(got) != 0 {
		t.Errorf("got (%v, %v), want (nil, nil)", got, err)
	}
}

// TestSameArtist_HappyPath: seed resolves to artist 1289;
// /artists/1289/releases returns 3 rows; bucket returns them as
// Candidates with the artist_id edge populated.
func TestSameArtist_HappyPath(t *testing.T) {
	mock := func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/artists/1289/releases") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"releases": []map[string]any{
				{"id": 1, "type": "release", "title": "Discovery", "artist": "Daft Punk", "year": 2001},
				{"id": 2, "type": "release", "title": "Homework", "artist": "Daft Punk", "year": 1997},
				{"id": 3, "type": "release", "title": "Random Access Memories", "artist": "Daft Punk", "year": 2013},
			},
		})
	}
	b := discogsbuckets.NewSameArtist(testClient(t, mock))
	got, err := b.Candidates(context.Background(), similarity.Seed{
		Title: "One More Time", Artist: "Daft Punk", DiscogsArtistID: 1289,
	})
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d candidates, want 3", len(got))
	}
	for _, c := range got {
		if c.Edge["discogs_artist_id"] != 1289 {
			t.Errorf("missing edge metadata on %+v", c)
		}
	}
}

// TestSameLabelEra_AppliesYearWindow: 5 releases with varied
// years; with seedYear=2000 and the bucket's ±3y window we
// expect 1997-2003 to survive, others dropped.
func TestSameLabelEra_AppliesYearWindow(t *testing.T) {
	mock := func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/labels/23528/releases") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"releases": []map[string]any{
				{"id": 10, "type": "release", "title": "T1", "artist": "A1", "year": 1995}, // out
				{"id": 11, "type": "release", "title": "T2", "artist": "A2", "year": 1998}, // in
				{"id": 12, "type": "release", "title": "T3", "artist": "A3", "year": 2000}, // in
				{"id": 13, "type": "release", "title": "T4", "artist": "A4", "year": 2003}, // in
				{"id": 14, "type": "release", "title": "T5", "artist": "A5", "year": 2008}, // out
				{"id": 15, "type": "release", "title": "T6", "artist": "A6", "year": 0},    // unknown → kept
			},
		})
	}
	b := discogsbuckets.NewSameLabelEra(testClient(t, mock))
	got, err := b.Candidates(context.Background(), similarity.Seed{
		Title: "X", Artist: "Y",
		DiscogsLabelID: 23528, Year: 2000,
	})
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(got) != 4 {
		t.Errorf("got %d candidates, want 4 (T2,T3,T4,T6)", len(got))
		for _, c := range got {
			t.Logf("  %s — %s", c.Artist, c.Title)
		}
	}
}

// TestSameLabelEra_BailsWithoutLabelOrYear: bucket needs both
// label_id AND year to be useful. Missing either → no candidates.
func TestSameLabelEra_BailsWithoutLabelOrYear(t *testing.T) {
	cases := []struct {
		name string
		seed similarity.Seed
	}{
		{"no label", similarity.Seed{Year: 2000}},
		{"no year", similarity.Seed{DiscogsLabelID: 23528}},
		{"neither", similarity.Seed{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := discogsbuckets.NewSameLabelEra(testClient(t, nil))
			got, err := b.Candidates(context.Background(), tc.seed)
			if err != nil || len(got) != 0 {
				t.Errorf("got (%v, %v), want (nil, nil)", got, err)
			}
		})
	}
}

// TestSameStyleEra_FansOutByStyle_AndYearFilters: seed has two
// styles; the bucket fires one search per style and filters each
// by the seed's year window. Verifies both the fan-out and the
// year-window math on style-based candidates.
func TestSameStyleEra_FansOutByStyle_AndYearFilters(t *testing.T) {
	// One mock responds to /database/search, branching on style.
	mock := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/database/search" {
			http.NotFound(w, r)
			return
		}
		style := r.URL.Query().Get("style")
		var rows []map[string]any
		switch style {
		case "House":
			rows = []map[string]any{
				{"id": 100, "title": "A1 - HouseHit", "year": "2000"}, // in
				{"id": 101, "title": "A2 - HouseOld", "year": "1990"}, // out
			}
		case "Disco":
			rows = []map[string]any{
				{"id": 200, "title": "B1 - DiscoHit", "year": "2001"}, // in
				{"id": 201, "title": "B2 - DiscoFuture", "year": "2010"}, // out
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"results": rows})
	}
	b := discogsbuckets.NewSameStyleEra(testClient(t, mock))
	got, err := b.Candidates(context.Background(), similarity.Seed{
		Title: "X", Artist: "Y",
		Year: 2000, Styles: []string{"House", "Disco"},
	})
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2 (HouseHit + DiscoHit)", len(got))
		for _, c := range got {
			t.Logf("  %s - %s", c.Artist, c.Title)
		}
	}
	// Edge metadata must carry the style that sourced the candidate,
	// so the v0.7 graph view can color edges accordingly.
	for _, c := range got {
		if c.Edge["discogs_style"] == nil {
			t.Errorf("missing discogs_style edge on %+v", c)
		}
	}
}

// TestSameStyleEra_BailsWithoutStylesOrYear: either missing
// attribute is a no-op, same bail-out pattern as same_label_era.
func TestSameStyleEra_BailsWithoutStylesOrYear(t *testing.T) {
	cases := []struct {
		name string
		seed similarity.Seed
	}{
		{"no styles", similarity.Seed{Year: 2000}},
		{"no year", similarity.Seed{Styles: []string{"House"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := discogsbuckets.NewSameStyleEra(testClient(t, nil))
			got, err := b.Candidates(context.Background(), tc.seed)
			if err != nil || len(got) != 0 {
				t.Errorf("got (%v, %v), want (nil, nil)", got, err)
			}
		})
	}
}

// TestCollaborators_CapsAt3_AndTagsEdge: seed has 5 collaborator
// IDs; bucket only fires /artists/{id}/releases for the first 3
// (collaboratorsCap). Each candidate's Edge must carry the
// specific collaborator id that sourced it.
func TestCollaborators_CapsAt3_AndTagsEdge(t *testing.T) {
	var calls []int
	var mu sync.Mutex
	mock := func(w http.ResponseWriter, r *http.Request) {
		// Path shape: /artists/{id}/releases
		var aid int
		if _, err := fmt.Sscanf(r.URL.Path, "/artists/%d/releases", &aid); err != nil {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		calls = append(calls, aid)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"releases": []map[string]any{
				{"id": aid * 10, "type": "release",
					"title": fmt.Sprintf("Release%d", aid),
					"artist": fmt.Sprintf("Artist%d", aid),
					"year":  2000},
			},
		})
	}
	b := discogsbuckets.NewCollaborators(testClient(t, mock))
	got, err := b.Candidates(context.Background(), similarity.Seed{
		Title:         "X",
		Artist:        "Y",
		Collaborators: []int{1, 2, 3, 4, 5},
	})
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("got %d candidates, want 3 (cap)", len(got))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 3 {
		t.Errorf("got %d /artists calls, want 3 (cap honored)", len(calls))
	}
	// Verify each candidate carries the right collaborator id.
	for _, c := range got {
		if _, ok := c.Edge["collaborator_discogs_id"].(int); !ok {
			t.Errorf("missing collaborator_discogs_id on %+v", c)
		}
	}
}

// TestCollaborators_BailsEmpty: common path — seed has no extra
// artists, bucket is a clean no-op without hitting Discogs.
func TestCollaborators_BailsEmpty(t *testing.T) {
	// Fail-loud handler — if bucket accidentally calls the API,
	// test reports which path was hit.
	mock := func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("should not have hit Discogs: %s", r.URL.Path)
		http.NotFound(w, r)
	}
	b := discogsbuckets.NewCollaborators(testClient(t, mock))
	got, err := b.Candidates(context.Background(), similarity.Seed{
		Title: "X", Artist: "Y", Collaborators: nil,
	})
	if err != nil || len(got) != 0 {
		t.Errorf("got (%v, %v), want (nil, nil)", got, err)
	}
}

// TestSameGenreEra_Works: genre-based fan-out mirrors style's
// logic; one genre, filter by year. Tests the whole wiring rather
// than re-verifying year math (covered by same_label_era).
func TestSameGenreEra_Works(t *testing.T) {
	mock := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/database/search" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("genre") != "Electronic" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"id": 300, "title": "A - E1", "year": "2000"},
				{"id": 301, "title": "B - E2", "year": "2001"},
				{"id": 302, "title": "C - E3", "year": "1990"}, // out of window
			},
		})
	}
	b := discogsbuckets.NewSameGenreEra(testClient(t, mock))
	got, err := b.Candidates(context.Background(), similarity.Seed{
		Title: "X", Artist: "Y",
		Year: 2000, Genres: []string{"Electronic"},
	})
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d, want 2 (E1 + E2; E3 out of ±3y window)", len(got))
	}
}

// testClient builds a *discogs.Client pointing at an httptest
// server with the given handler. Caches go to nil (no SQLite in
// tests). Limiter is wide-open so tests don't slow on rate
// budget.
func testClient(t *testing.T, h http.HandlerFunc) *discogs.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return discogs.NewClient(srv.URL, "tok", nil,
		discogs.WithRand(rand.New(rand.NewSource(1))), //nolint:gosec
		discogs.WithLimiter(100, 100),
	)
}
