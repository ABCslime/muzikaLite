package similarity

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/httpx"
)

// Handler exposes the per-user similar-mode toggle:
//
//   GET  /api/queue/similar-mode → {"seedSongId": "..."} or null
//   POST /api/queue/similar-mode → body {"seedSongId": "..." | null}
//
// Both routes are protected (httpx.WithAuth-wrapped at mount).
// Setting an empty / null seedSongId clears the mode and the
// refiller falls back to genre-random on the next Trigger.
type Handler struct {
	repo *Repo
}

// NewHandler constructs the HTTP handler over a Repo. Pass the
// same Repo wired into the SimilarMode adapter on the refiller —
// otherwise the GET state and the refiller's read can diverge.
func NewHandler(r *Repo) *Handler { return &Handler{repo: r} }

// SimilarModeResponse is the GET reply shape. seedSongId is the
// JSON-stringified UUID, or null when similar mode is off.
// Frontend reads the "active" boolean as the visual indicator
// for the lens icon.
type SimilarModeResponse struct {
	SeedSongID *string `json:"seedSongId"`
	Active     bool    `json:"active"`
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
	resp := SimilarModeResponse{Active: seed != uuid.Nil}
	if seed != uuid.Nil {
		s := seed.String()
		resp.SeedSongID = &s
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}
