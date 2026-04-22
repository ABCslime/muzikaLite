package discogs_test

import (
	"context"
	"encoding/json"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
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
