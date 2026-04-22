package similarity

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/httpx"
)

// Handler exposes the per-user similar-mode toggle + the bucket
// weight settings surface:
//
//   GET  /api/queue/similar-mode  → {"seedSongId": "..."} or null
//   POST /api/queue/similar-mode  → body {"seedSongId": "..."|null}
//   GET  /api/similarity/buckets  → registered bucket metadata
//   GET  /api/similarity/weights  → user's tuned weights (map)
//   PUT  /api/similarity/weights  → replace user's weights
//
// All routes are protected (httpx.WithAuth-wrapped at mount).
type Handler struct {
	repo *Repo
	svc  *Service // used for the bucket registry snapshot
}

// NewHandler constructs the HTTP handler over a Repo and Service.
// The Service reference gives the handler access to the bucket
// registry for GET /api/similarity/buckets; passing the Repo
// separately (even though Service.weights == Repo today) keeps
// the seed-mode routes testable without constructing a Service.
func NewHandler(r *Repo, s *Service) *Handler { return &Handler{repo: r, svc: s} }

// SimilarModeResponse is the GET reply shape. Active is the
// single boolean the frontend uses for "similar mode on/off"
// state. SeedSongID + SeedTitle + SeedArtist are the
// v0.5-compatible singular fields (first element of the seed
// set); Seeds is the full v0.6 multi-seed list.
//
// Backward compat: v0.5 clients that only know seedSongId keep
// working — it's the first entry of Seeds, stable under a
// deterministic order. New v0.6+ clients read Seeds for the
// full list.
type SimilarModeResponse struct {
	Active     bool        `json:"active"`
	SeedSongID *string     `json:"seedSongId"`
	SeedTitle  string      `json:"seedTitle,omitempty"`
	SeedArtist string      `json:"seedArtist,omitempty"`
	LastError  string      `json:"lastError,omitempty"`
	Seeds      []SeedEntry `json:"seeds"`
}

// SeedEntry is one row in the multi-seed list returned to the
// frontend. Includes enough metadata for the Home-view chip
// renderer so it doesn't need a second fetch.
type SeedEntry struct {
	ID     string `json:"id"`
	Title  string `json:"title,omitempty"`
	Artist string `json:"artist,omitempty"`
}

// SimilarModeRequest is the POST body for the "replace the
// entire seed set" route. Supports both shapes:
//
//   v0.5 singular: {"seedSongId": "uuid"}  — replaces set with [uuid]
//   v0.5 clear:    {"seedSongId": null}    — clears set
//   v0.6 multi:    {"seedSongIds": ["u1","u2"]} — replaces set
//
// When both fields are present SeedSongIDs wins. Missing both =
// empty set = clear mode.
type SimilarModeRequest struct {
	SeedSongID  *string  `json:"seedSongId"`
	SeedSongIDs []string `json:"seedSongIds"`
}

// Get returns the current similar-mode state for the caller.
// Populates both the v0.6 Seeds array (all seeds with metadata)
// and the v0.5-compat singular fields (first seed's id/title/
// artist for backward compat).
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpx.GetUserID(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	seeds, err := h.repo.SeedsFor(r.Context(), userID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "read similar mode failed")
		return
	}
	resp := SimilarModeResponse{
		Active: len(seeds) > 0,
		Seeds:  make([]SeedEntry, 0, len(seeds)),
	}
	for i, s := range seeds {
		entry := SeedEntry{ID: s.String()}
		// Hydrate metadata via the same adapter the engine uses
		// for picks. Failures (Bandcamp-only seed, dead row) are
		// tolerated — the frontend renders the id as a fallback
		// and the chip's tooltip explains via lastError.
		if h.svc != nil {
			if meta, err := h.svc.ReadSeedMetadata(r.Context(), userID, s); err == nil {
				entry.Title = meta.Title
				entry.Artist = meta.Artist
			}
		}
		resp.Seeds = append(resp.Seeds, entry)
		// v0.5-compat singular fields — first seed wins.
		if i == 0 {
			id := s.String()
			resp.SeedSongID = &id
			resp.SeedTitle = entry.Title
			resp.SeedArtist = entry.Artist
		}
	}
	if resp.Active && h.svc != nil {
		resp.LastError = h.svc.LastError(userID)
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// Set replaces the entire similar-mode seed set. Accepts both
// v0.5 singular (seedSongId) and v0.6 multi (seedSongIds) body
// shapes; multi wins when both are supplied. Empty body or
// nulls on both fields = clear similar mode.
func (h *Handler) Set(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpx.GetUserID(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	var req SimilarModeRequest
	// Empty body is valid (= clear). json.NewDecoder.Decode on
	// an empty body returns io.EOF; treat that as the zero
	// request.
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	seeds, err := parseSeedList(req)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.repo.ReplaceSeeds(r.Context(), userID, seeds); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "set similar mode failed")
		return
	}
	// v0.5 PR E: any seed change invalidates the prior lastError.
	if h.svc != nil {
		h.svc.ClearLastError(userID)
	}
	h.writeCurrent(w, r, userID)
}

// AddSeed appends one song to the user's seed set. Idempotent
// on the backend. POST /api/queue/similar-mode/seeds/{songId}.
func (h *Handler) AddSeed(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpx.GetUserID(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	songID, err := uuid.Parse(r.PathValue("songId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid songId")
		return
	}
	if err := h.repo.AddSeed(r.Context(), userID, songID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "add seed failed")
		return
	}
	if h.svc != nil {
		h.svc.ClearLastError(userID)
	}
	h.writeCurrent(w, r, userID)
}

// RemoveSeed drops one song from the user's seed set. Idempotent.
// DELETE /api/queue/similar-mode/seeds/{songId}.
func (h *Handler) RemoveSeed(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpx.GetUserID(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	songID, err := uuid.Parse(r.PathValue("songId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid songId")
		return
	}
	if err := h.repo.RemoveSeed(r.Context(), userID, songID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "remove seed failed")
		return
	}
	if h.svc != nil {
		h.svc.ClearLastError(userID)
	}
	h.writeCurrent(w, r, userID)
}

// writeCurrent is the shared "reply with the current state"
// tail of Set / AddSeed / RemoveSeed. Extracting it keeps the
// three handlers in lockstep — all mutations return the full
// freshly-read state including Seeds metadata.
func (h *Handler) writeCurrent(w http.ResponseWriter, r *http.Request, _ uuid.UUID) {
	// Delegate to Get's assembly logic. A second query is
	// cheap; the alternative is reshaping Get's body into a
	// helper + duplicating the error-handling wrapper.
	h.Get(w, r)
}

// parseSeedList extracts the target seed-set slice from a
// SimilarModeRequest. Multi (seedSongIds) wins over singular
// (seedSongId) when both are present. Returns the parsed slice
// or an error for malformed UUIDs.
func parseSeedList(req SimilarModeRequest) ([]uuid.UUID, error) {
	if len(req.SeedSongIDs) > 0 {
		out := make([]uuid.UUID, 0, len(req.SeedSongIDs))
		for _, s := range req.SeedSongIDs {
			if s == "" {
				continue
			}
			id, err := uuid.Parse(s)
			if err != nil {
				return nil, fmt.Errorf("invalid seedSongIds[]: %s", s)
			}
			out = append(out, id)
		}
		return out, nil
	}
	if req.SeedSongID != nil && *req.SeedSongID != "" {
		id, err := uuid.Parse(*req.SeedSongID)
		if err != nil {
			return nil, fmt.Errorf("invalid seedSongId")
		}
		return []uuid.UUID{id}, nil
	}
	return nil, nil
}

// BucketInfo is the JSON shape for one row in
// GET /api/similarity/buckets. The frontend's settings UI uses
// this to render one slider per bucket — label + description for
// the label, defaultWeight for the slider's reset target, id as
// the write key on PUT /api/similarity/weights.
type BucketInfo struct {
	ID            string  `json:"id"`
	Label         string  `json:"label"`
	Description   string  `json:"description"`
	DefaultWeight float64 `json:"defaultWeight"`
}

// ListBuckets responds with the current bucket registry. Order
// matches main.go's Register call order, which we use to group
// related buckets in the UI (artist/label/style/collab/genre).
//
// Empty registry (Discogs disabled, plugins not loaded) returns
// an empty array — the frontend renders "no buckets registered"
// rather than pretending to have sliders.
func (h *Handler) ListBuckets(w http.ResponseWriter, r *http.Request) {
	if h.svc == nil {
		httpx.WriteJSON(w, http.StatusOK, []BucketInfo{})
		return
	}
	bs := h.svc.Buckets()
	out := make([]BucketInfo, 0, len(bs))
	for _, b := range bs {
		out = append(out, BucketInfo{
			ID:            b.ID(),
			Label:         b.Label(),
			Description:   b.Description(),
			DefaultWeight: b.DefaultWeight(),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// GetWeights returns the user's tuned bucket weights. Missing
// (no row / NULL / empty) returns an empty object, which the
// frontend renders as "all sliders at their defaults."
func (h *Handler) GetWeights(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpx.GetUserID(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	weights, err := h.repo.WeightsFor(r.Context(), userID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "read weights failed")
		return
	}
	if weights == nil {
		weights = map[string]float64{}
	}
	httpx.WriteJSON(w, http.StatusOK, weights)
}

// PutWeights replaces the user's bucket_weights JSON with the
// body map. Empty/missing body = clear to defaults. We don't
// partial-merge — a PUT is a full replace, consistent with the
// user_preferences PUT semantics.
//
// No validation of bucket IDs against the registry: v0.6 plugins
// may register new IDs after the user has set their weights, and
// we want those weights to survive a plugin restart. Unknown IDs
// in the stored map become inert (the engine only reads keys that
// match a registered bucket).
func (h *Handler) PutWeights(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpx.GetUserID(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	var body map[string]float64
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if err := h.repo.SetWeights(r.Context(), userID, body); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "write weights failed")
		return
	}
	if body == nil {
		body = map[string]float64{}
	}
	httpx.WriteJSON(w, http.StatusOK, body)
}
