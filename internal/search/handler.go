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
// Response shape (v0.4.2 PR B):
//
//	{
//	  "genres":   ["Electronic", ...],
//	  "artists":  [{id, name, thumb?}, ...],
//	  "releases": [{title, artist, catalogNumber?, year?}, ...],
//	  "labels":   [{id, name, thumb?}, ...]
//	}
//
// Every section is always present (empty array when no hits). The
// frontend treats an empty section as "hide its header".
//
// Query semantics:
//   - empty q         → 200 with an all-empty preview (UI hides dropdown)
//   - Discogs off     → 503           (actionable — flip MUZIKA_DISCOGS_ENABLED)
//   - Discogs 429     → 503 Retry-After (passes upstream rate-limit through)
//   - Discogs 5xx/err → 502 (only if ALL three parallel sections failed;
//                             partial failures are logged and degraded silently)
func (h *Handler) Preview(w http.ResponseWriter, r *http.Request) {
	if _, ok := httpx.GetUserID(r.Context()); !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	q := r.URL.Query().Get("q")
	preview, err := h.prev.Preview(r.Context(), q)
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
	httpx.WriteJSON(w, http.StatusOK, preview)
}
