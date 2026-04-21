package search

import (
	"errors"
	"net/http"

	"github.com/macabc/muzika/internal/discogs"
	"github.com/macabc/muzika/internal/httpx"
)

// Handler mounts GET /api/queue/search/preview.
type Handler struct{ prev *Previewer }

// NewHandler constructs a Handler.
func NewHandler(p *Previewer) *Handler { return &Handler{prev: p} }

// Preview handles GET /api/queue/search/preview?q=<query> (protected).
//
// Query semantics:
//   - empty q         → 200 with []   (UI hides the dropdown)
//   - Discogs off     → 503           (actionable — flip MUZIKA_DISCOGS_ENABLED)
//   - Discogs 429     → 503 Retry-After (passes upstream rate-limit through)
//   - Discogs 5xx/err → 502
//   - success         → 200 with [{title, artist, catalogNumber, year}, ...]
//
// The endpoint is read-only: no stub, no queue state, no side effects
// on failure. A typeahead firing on every keystroke can hit this hard
// — Discogs' 30-day SQLite cache absorbs repeat queries without
// re-hitting the API.
func (h *Handler) Preview(w http.ResponseWriter, r *http.Request) {
	if _, ok := httpx.GetUserID(r.Context()); !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	q := r.URL.Query().Get("q")
	results, err := h.prev.Preview(r.Context(), q)
	if err != nil {
		switch {
		case errors.Is(err, ErrDiscogsDisabled):
			httpx.WriteError(w, http.StatusServiceUnavailable,
				"search unavailable — Discogs integration is not enabled")
		case errors.Is(err, discogs.ErrRateLimited):
			w.Header().Set("Retry-After", "5")
			httpx.WriteError(w, http.StatusServiceUnavailable,
				"upstream rate limit; retry shortly")
		default:
			httpx.WriteError(w, http.StatusBadGateway, "search backend error")
		}
		return
	}
	if results == nil {
		results = []Candidate{}
	}
	httpx.WriteJSON(w, http.StatusOK, results)
}
