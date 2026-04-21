package preferences

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/macabc/muzika/internal/httpx"
)

// Handler mounts /api/user/preferences.
type Handler struct{ svc *Service }

// NewHandler constructs a Handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Get handles GET /api/user/preferences (protected).
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpx.GetUserID(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	p, err := h.svc.Get(r.Context(), userID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "preferences fetch failed")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

// Put handles PUT /api/user/preferences (protected). Body is the
// Preferences shape. Empty lists are valid — they mean "clear my
// preferences for that source" and the refiller falls back to defaults.
func (h *Handler) Put(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpx.GetUserID(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	var req Preferences
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if err := h.svc.Replace(r.Context(), userID, req); err != nil {
		switch {
		case errors.Is(err, ErrTooMany):
			httpx.WriteError(w, http.StatusBadRequest, "too many items for one source")
		case errors.Is(err, ErrItemTooLong):
			httpx.WriteError(w, http.StatusBadRequest, "item string too long")
		default:
			httpx.WriteError(w, http.StatusInternalServerError, "preferences write failed")
		}
		return
	}
	// Return the normalized form the server actually persisted so the
	// client can reconcile (dedupe, empty-row strip) without a second GET.
	p, err := h.svc.Get(r.Context(), userID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "post-write fetch failed")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}
