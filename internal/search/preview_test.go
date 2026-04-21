package search_test

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/macabc/muzika/internal/discogs"
	"github.com/macabc/muzika/internal/search"
)

// TestPreview_EmptyQueryReturnsNilNoError: typeahead UX — an empty input
// means "hide the dropdown"; the handler should not treat it as an error.
func TestPreview_EmptyQueryReturnsNilNoError(t *testing.T) {
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
	if res != nil {
		t.Errorf("expected nil for empty query, got %v", res)
	}
}

// TestPreview_ReturnsUpToLimitCandidates: a well-populated response is
// truncated to the Previewer's default limit (10). Each row uses a
// distinct (artist, title) because the dedup introduced in v0.4.2 PR A
// collapses same-pair rows — we need 25 distinct pairs to actually
// exercise the limit.
func TestPreview_ReturnsUpToLimitCandidates(t *testing.T) {
	items := make([]map[string]any, 25)
	for i := range items {
		items[i] = map[string]any{
			"title": "Artist" + string(rune('A'+i)) + " - Release" + string(rune('A'+i)),
			"catno": "CAT-" + string(rune('A'+i%26)),
			"year":  "2020",
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/database/search" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"results": items})
	}))
	defer srv.Close()

	c := discogs.NewClient(srv.URL, "tok", nil,
		discogs.WithRand(rand.New(rand.NewSource(1))),
		discogs.WithLimiter(100, 100),
	)
	p := search.NewPreviewer(c)

	res, err := p.Preview(context.Background(), "anything")
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if len(res) != 10 {
		t.Errorf("got %d candidates, want 10 (default limit)", len(res))
	}
	if res[0].Year != 2020 {
		t.Errorf("year not parsed: %d", res[0].Year)
	}
	if res[0].Artist != "ArtistA" || res[0].Title != "ReleaseA" {
		t.Errorf("split failed: %+v", res[0])
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
