package auth

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/httpx"
)

// Handler wraps Service with HTTP adapters.
type Handler struct {
	svc *Service
}

// NewHandler constructs an HTTP adapter for Service.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register handles POST /api/auth/user (public).
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	u, err := h.svc.Register(r.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidUsername),
			errors.Is(err, ErrInvalidPassword),
			errors.Is(err, ErrInvalidEmail):
			httpx.WriteError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, ErrDuplicate):
			httpx.WriteError(w, http.StatusConflict, err.Error())
		default:
			httpx.WriteError(w, http.StatusInternalServerError, "registration failed")
		}
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, UserResponse{
		ID:        u.ID,
		Username:  u.Username,
		Email:     u.Email,
		CreatedAt: u.CreatedAt,
	})
}

// Login handles POST /api/auth/login (public).
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	resp, err := h.svc.Login(r.Context(), req)
	if err != nil {
		if errors.Is(err, ErrBadCredentials) {
			httpx.WriteError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "login failed")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// Delete handles DELETE /api/auth/user/{id} (protected).
// Authorizes that {id} equals the authenticated caller — no admin path exists.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	callerID, ok := httpx.GetUserID(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	targetID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if callerID != targetID {
		httpx.WriteError(w, http.StatusForbidden, "cannot delete another user")
		return
	}
	if err := h.svc.Delete(r.Context(), targetID); err != nil {
		if errors.Is(err, ErrNotFound) {
			httpx.WriteError(w, http.StatusNotFound, "user not found")
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// LogoutAll handles POST /api/auth/logout-all (protected). Bumps the caller's
// token_version so every outstanding JWT fails Verify.
func (h *Handler) LogoutAll(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpx.GetUserID(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	if err := h.svc.LogoutAll(r.Context(), userID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "logout-all failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
