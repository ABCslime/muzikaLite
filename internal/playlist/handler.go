package playlist

import (
	"net/http"

	"github.com/macabc/muzika/internal/httpx"
)

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// List handles GET /api/playlist/ (protected). TODO(port): Phase 5.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, http.StatusNotImplemented, "playlist: List not implemented")
}

// Get handles GET /api/playlist/{id} (protected). TODO(port): Phase 5.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, http.StatusNotImplemented, "playlist: Get not implemented")
}

// Create handles POST /api/playlist/ (protected). TODO(port): Phase 5.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, http.StatusNotImplemented, "playlist: Create not implemented")
}

// Delete handles DELETE /api/playlist/{id} (protected). TODO(port): Phase 5.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, http.StatusNotImplemented, "playlist: Delete not implemented")
}

// AddSong handles POST /api/playlist/{id}/song/{songId} (protected). TODO(port): Phase 5.
func (h *Handler) AddSong(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, http.StatusNotImplemented, "playlist: AddSong not implemented")
}

// RemoveSong handles DELETE /api/playlist/{id}/song/{songId} (protected). TODO(port): Phase 5.
func (h *Handler) RemoveSong(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, http.StatusNotImplemented, "playlist: RemoveSong not implemented")
}
