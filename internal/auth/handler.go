package auth

import (
	"net/http"

	"github.com/macabc/muzika/internal/httpx"
)

// Handler wraps Service with HTTP adapters.
type Handler struct {
	svc *Service
}

// NewHandler constructs an HTTP adapter for Service.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register handles POST /api/auth/user (public).
// TODO(port): Phase 4.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, http.StatusNotImplemented, "auth: Register not implemented")
}

// Login handles POST /api/auth/login (public).
// TODO(port): Phase 4.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, http.StatusNotImplemented, "auth: Login not implemented")
}

// Delete handles DELETE /api/auth/user/{id} (protected).
// TODO(port): Phase 4.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, http.StatusNotImplemented, "auth: Delete not implemented")
}

// LogoutAll handles POST /api/auth/logout-all (protected).
// TODO(port): Phase 4.
func (h *Handler) LogoutAll(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, http.StatusNotImplemented, "auth: LogoutAll not implemented")
}
