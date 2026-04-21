package search

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/macabc/muzika/internal/discogs"
	"github.com/macabc/muzika/internal/httpx"
)

// availabilityByArtistDeadline caps how long one by-artist request
// can occupy a goroutine. The deadline lives on the handler (not
// deeper in CheckByArtistAvailability) so it applies to every code
// path — broad search, per-title fallback, and the in-flight gosk
// goroutines we cancel via ctx. 15 s fits a 10 s broad window plus
// a few seconds of trickle fallback without stretching the UX.
const availabilityByArtistDeadline = 15 * time.Second

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

// Artist handles GET /api/discogs/artist/{id} (protected). v0.4.2 PR C.
// Returns the artist's releases as Candidates the frontend can queue
// via the existing searchAcquire path.
func (h *Handler) Artist(w http.ResponseWriter, r *http.Request) {
	id, ok := h.entityID(w, r)
	if !ok {
		return
	}
	detail, err := h.prev.Artist(r.Context(), id)
	if err != nil {
		h.writeDetailErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, detail)
}

// Label handles GET /api/discogs/label/{id} (protected).
func (h *Handler) Label(w http.ResponseWriter, r *http.Request) {
	id, ok := h.entityID(w, r)
	if !ok {
		return
	}
	detail, err := h.prev.Label(r.Context(), id)
	if err != nil {
		h.writeDetailErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, detail)
}

// Release handles GET /api/discogs/release/{id} (protected). Returns
// metadata + tracklist for the AlbumView.
func (h *Handler) Release(w http.ResponseWriter, r *http.Request) {
	id, ok := h.entityID(w, r)
	if !ok {
		return
	}
	detail, err := h.prev.Release(r.Context(), id)
	if err != nil {
		h.writeDetailErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, detail)
}

// entityID pulls the {id} path param and validates it. 400 on garbage.
func (h *Handler) entityID(w http.ResponseWriter, r *http.Request) (int, bool) {
	if _, ok := httpx.GetUserID(r.Context()); !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "not authenticated")
		return 0, false
	}
	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}
	return id, true
}

// writeDetailErr maps Previewer/Discogs errors to the same HTTP code
// layout the Preview endpoint uses so the frontend can handle all
// detail routes uniformly.
func (h *Handler) writeDetailErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrDiscogsDisabled):
		httpx.WriteError(w, http.StatusServiceUnavailable,
			"Discogs integration is not enabled")
	case errors.Is(err, discogs.ErrNoResults):
		httpx.WriteError(w, http.StatusNotFound, "not found")
	case errors.Is(err, discogs.ErrRateLimited):
		w.Header().Set("Retry-After", "5")
		httpx.WriteError(w, http.StatusServiceUnavailable,
			"upstream rate limit; retry shortly")
	default:
		httpx.WriteError(w, http.StatusBadGateway, "search backend error")
	}
}

// Availability handles POST /api/queue/search/availability (v0.4.2 PR D).
// Body: {"items":[{"title","artist","catalogNumber?"}...]}.
// Response: {"results":[{"available":bool,"peerCount":int}...]} in input order.
//
// Cap request size to 100 items so a malformed label page with 10k
// releases can't fan out into 10k goroutines. The backend's per-item
// probe is 2 s wall; 100 items at 10-way concurrency is ~20 s, the
// effective timeout we want to allow. Larger pages should paginate
// client-side.
const maxAvailabilityItems = 100

func (h *Handler) Availability(w http.ResponseWriter, r *http.Request) {
	if _, ok := httpx.GetUserID(r.Context()); !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	var req struct {
		Items []AvailabilityQuery `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if len(req.Items) > maxAvailabilityItems {
		httpx.WriteError(w, http.StatusBadRequest, "too many items")
		return
	}
	results, err := h.prev.CheckAvailability(r.Context(), req.Items)
	if err != nil {
		if errors.Is(err, ErrSoulseekDisabled) {
			httpx.WriteError(w, http.StatusServiceUnavailable,
				"Soulseek backend is not configured")
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "availability check failed")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"results": results})
}

// AvailabilityByArtist handles POST /api/queue/search/availability/by-artist
// (v0.4.2 PR E). Body: {"artist":"...","titles":["...","..."]}.
// Response: {"results":[{"available":bool,"peerCount":int}...]} in input order.
//
// One Soulseek search for the whole artist, then filematch-filter per
// title. More efficient and more reliable than the per-release probe
// for artist/album pages where every row shares an artist.
func (h *Handler) AvailabilityByArtist(w http.ResponseWriter, r *http.Request) {
	if _, ok := httpx.GetUserID(r.Context()); !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	var req ArtistAvailabilityQuery
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if len(req.Titles) > maxAvailabilityItems {
		httpx.WriteError(w, http.StatusBadRequest, "too many titles")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), availabilityByArtistDeadline)
	defer cancel()
	results, err := h.prev.CheckByArtistAvailability(ctx, req.Artist, req.Titles)
	if err != nil {
		if errors.Is(err, ErrSoulseekDisabled) {
			httpx.WriteError(w, http.StatusServiceUnavailable,
				"Soulseek backend is not configured")
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "availability check failed")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"results": results})
}
