package queue

import (
	"net/http"

	"github.com/macabc/muzika/internal/httpx"
)

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// GetQueue handles GET /api/queue/queue (protected). TODO(port): Phase 6.
func (h *Handler) GetQueue(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, http.StatusNotImplemented, "queue: GetQueue not implemented")
}

// AddSong handles POST /api/queue/queue (protected). TODO(port): Phase 6.
func (h *Handler) AddSong(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, http.StatusNotImplemented, "queue: AddSong not implemented")
}

// Check handles POST /api/queue/queue/check (protected). TODO(port): Phase 6.
func (h *Handler) Check(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, http.StatusNotImplemented, "queue: Check not implemented")
}

// Skipped handles POST /api/queue/queue/skipped (protected). TODO(port): Phase 6.
func (h *Handler) Skipped(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, http.StatusNotImplemented, "queue: Skipped not implemented")
}

// Finished handles POST /api/queue/queue/finished (protected). TODO(port): Phase 6.
func (h *Handler) Finished(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, http.StatusNotImplemented, "queue: Finished not implemented")
}

// StreamSong handles GET /api/queue/songs/{id} (protected). TODO(port): Phase 6.
// Will use http.ServeContent + mime.TypeByExtension.
func (h *Handler) StreamSong(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, http.StatusNotImplemented, "queue: StreamSong not implemented")
}

// IsLiked handles GET /api/queue/songs/{id}/liked (protected). TODO(port): Phase 6.
func (h *Handler) IsLiked(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, http.StatusNotImplemented, "queue: IsLiked not implemented")
}

// Like handles POST /api/queue/songs/{id}/liked (protected). TODO(port): Phase 6.
func (h *Handler) Like(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, http.StatusNotImplemented, "queue: Like not implemented")
}

// Unlike handles POST /api/queue/songs/{id}/unliked (protected). TODO(port): Phase 6.
func (h *Handler) Unlike(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, http.StatusNotImplemented, "queue: Unlike not implemented")
}
