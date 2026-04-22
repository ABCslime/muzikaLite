package similarity

import (
	"encoding/json"
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

// SimilarModeResponse is the GET reply shape. seedSongId is the
// JSON-stringified UUID, or null when similar mode is off.
// Frontend reads the "active" boolean as the visual indicator
// for the lens icon.
//
// v0.5 PR E: LastError surfaces the most recent NextPick failure
// reason from the similarity service so the lens icon can render
// a third "active but not working" state (orange) when the seed
// has no Discogs match or all buckets came back empty. Empty
// string = last cycle succeeded (or no cycle has run yet, which
// the frontend treats as "assume OK until proven otherwise").
//
// v0.5 PR F: SeedTitle + SeedArtist are populated when a seed is
// active. Lets the Home view render a chip ("Similar: <artist> —
// <title>") without a second round-trip to fetch the song. Empty
// strings when similar mode is off; populated from queue_songs
// via the same SeedReader adapter the engine uses.
type SimilarModeResponse struct {
	SeedSongID *string `json:"seedSongId"`
	Active     bool    `json:"active"`
	LastError  string  `json:"lastError,omitempty"`
	SeedTitle  string  `json:"seedTitle,omitempty"`
	SeedArtist string  `json:"seedArtist,omitempty"`
}

// SimilarModeRequest is the POST body. SeedSongID nil OR empty
// string clears the mode; non-empty UUID sets it.
type SimilarModeRequest struct {
	SeedSongID *string `json:"seedSongId"`
}

// Get returns the current similar-mode state for the caller.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpx.GetUserID(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	seed, err := h.repo.SeedFor(r.Context(), userID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "read similar mode failed")
		return
	}
	resp := SimilarModeResponse{Active: seed != uuid.Nil}
	if seed != uuid.Nil {
		s := seed.String()
		resp.SeedSongID = &s
		if h.svc != nil {
			resp.LastError = h.svc.LastError(userID)
			// v0.5 PR F: hydrate the seed's (title, artist) for the
			// Home-view chip. Same adapter the engine uses for the
			// refill path — so an inconsistent "chip shows one thing
			// but picks are made from another" is impossible.
			// Hydration failure is silently tolerated: the chip just
			// renders with the id as a fallback label.
			if seedMeta, err := h.svc.ReadSeedMetadata(r.Context(), userID, seed); err == nil {
				resp.SeedTitle = seedMeta.Title
				resp.SeedArtist = seedMeta.Artist
			}
		}
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// Set updates the similar-mode seed. POSTing a body with
// seedSongId=null (or omitted) clears the mode.
func (h *Handler) Set(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpx.GetUserID(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	var req SimilarModeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	var seed uuid.UUID
	if req.SeedSongID != nil && *req.SeedSongID != "" {
		parsed, err := uuid.Parse(*req.SeedSongID)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid seedSongId")
			return
		}
		seed = parsed
	}
	if err := h.repo.SetSeed(r.Context(), userID, seed); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "set similar mode failed")
		return
	}
	// v0.5 PR E: changing the seed invalidates any prior error —
	// the new seed hasn't been tried yet. Clear so the lens flips
	// out of orange immediately rather than waiting for a fresh
	// successful cycle.
	if h.svc != nil {
		h.svc.ClearLastError(userID)
	}
	resp := SimilarModeResponse{Active: seed != uuid.Nil}
	if seed != uuid.Nil {
		s := seed.String()
		resp.SeedSongID = &s
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
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
