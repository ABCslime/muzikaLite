package playlist

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/httpx"
)

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// List handles GET /api/playlist/ (protected).
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpx.GetUserID(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	out, err := h.svc.ListForUser(r.Context(), userID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "list failed")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// Get handles GET /api/playlist/{id} (protected).
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID, pid, ok := requireUserAndID(w, r, "id")
	if !ok {
		return
	}
	resp, err := h.svc.Get(r.Context(), userID, pid)
	if err != nil {
		writeDomainErr(w, err, "get failed")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// Create handles POST /api/playlist/ (protected).
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpx.GetUserID(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	var req CreatePlaylistRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	p, err := h.svc.Create(r.Context(), userID, req)
	if err != nil {
		if errors.Is(err, ErrInvalidName) {
			httpx.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "create failed")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, p)
}

// Delete handles DELETE /api/playlist/{id} (protected).
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	userID, pid, ok := requireUserAndID(w, r, "id")
	if !ok {
		return
	}
	if err := h.svc.Delete(r.Context(), userID, pid); err != nil {
		writeDomainErr(w, err, "delete failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// AddSong handles POST /api/playlist/{id}/song/{songId} (protected).
func (h *Handler) AddSong(w http.ResponseWriter, r *http.Request) {
	userID, pid, sid, ok := requireUserAndTwoIDs(w, r)
	if !ok {
		return
	}
	if err := h.svc.AddSong(r.Context(), userID, pid, sid); err != nil {
		writeDomainErr(w, err, "add song failed")
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// RemoveSong handles DELETE /api/playlist/{id}/song/{songId} (protected).
func (h *Handler) RemoveSong(w http.ResponseWriter, r *http.Request) {
	userID, pid, sid, ok := requireUserAndTwoIDs(w, r)
	if !ok {
		return
	}
	if err := h.svc.RemoveSong(r.Context(), userID, pid, sid); err != nil {
		writeDomainErr(w, err, "remove song failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers ---

func requireUserAndID(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, uuid.UUID, bool) {
	userID, ok := httpx.GetUserID(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "not authenticated")
		return uuid.Nil, uuid.Nil, false
	}
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid "+name)
		return uuid.Nil, uuid.Nil, false
	}
	return userID, id, true
}

func requireUserAndTwoIDs(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, uuid.UUID, bool) {
	userID, pid, ok := requireUserAndID(w, r, "id")
	if !ok {
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	sid, err := uuid.Parse(r.PathValue("songId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid songId")
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	return userID, pid, sid, true
}

func writeDomainErr(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrForbidden):
		httpx.WriteError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, ErrDuplicate):
		httpx.WriteError(w, http.StatusConflict, err.Error())
	default:
		httpx.WriteError(w, http.StatusInternalServerError, fallback)
	}
}
