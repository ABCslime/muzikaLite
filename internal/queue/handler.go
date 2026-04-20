package queue

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"os"
	"path/filepath"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/httpx"
)

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// GetQueue handles GET /api/queue/queue (protected).
func (h *Handler) GetQueue(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpx.GetUserID(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	resp, err := h.svc.GetQueue(r.Context(), userID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "queue fetch failed")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// AddSong handles POST /api/queue/queue (protected).
func (h *Handler) AddSong(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpx.GetUserID(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	var req AddSongRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if err := h.svc.AddSong(r.Context(), userID, req); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "add failed")
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// Check handles POST /api/queue/queue/check (protected).
func (h *Handler) Check(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpx.GetUserID(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	_ = h.svc.CheckQueue(r.Context(), userID)
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "refill triggered"})
}

// Skipped handles POST /api/queue/queue/skipped (protected).
func (h *Handler) Skipped(w http.ResponseWriter, r *http.Request) {
	userID, req, ok := decodeUserAndSongReq(w, r)
	if !ok {
		return
	}
	if err := h.svc.MarkSkipped(r.Context(), userID, req); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "skip failed")
		return
	}
	w.WriteHeader(http.StatusOK)
}

// Finished handles POST /api/queue/queue/finished (protected).
func (h *Handler) Finished(w http.ResponseWriter, r *http.Request) {
	userID, req, ok := decodeUserAndSongReq(w, r)
	if !ok {
		return
	}
	if err := h.svc.MarkFinished(r.Context(), userID, req); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "finish failed")
		return
	}
	w.WriteHeader(http.StatusOK)
}

// StreamSong handles GET /api/queue/songs/{id} (protected). Uses
// http.ServeContent so Range requests work, and sniffs Content-Type from
// the file extension.
func (h *Handler) StreamSong(w http.ResponseWriter, r *http.Request) {
	if _, ok := httpx.GetUserID(r.Context()); !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	songID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid song id")
		return
	}
	path, err := h.svc.ResolveSongPath(r.Context(), songID)
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			httpx.WriteError(w, http.StatusNotFound, "song not found")
		case errors.Is(err, ErrNoFile):
			httpx.WriteError(w, http.StatusNotFound, "song file not available yet")
		default:
			httpx.WriteError(w, http.StatusInternalServerError, "resolve failed")
		}
		return
	}
	f, err := os.Open(path)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "song file not found on disk")
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "stat failed")
		return
	}
	ct := mime.TypeByExtension(filepath.Ext(path))
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	http.ServeContent(w, r, filepath.Base(path), stat.ModTime(), f)
}

// IsLiked handles GET /api/queue/songs/{id}/liked (protected).
func (h *Handler) IsLiked(w http.ResponseWriter, r *http.Request) {
	userID, songID, ok := userAndSongID(w, r)
	if !ok {
		return
	}
	liked, err := h.svc.IsLiked(r.Context(), userID, songID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "liked lookup failed")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, SongLikedResponse{Liked: liked})
}

// Like handles POST /api/queue/songs/{id}/liked (protected).
func (h *Handler) Like(w http.ResponseWriter, r *http.Request) {
	userID, songID, ok := userAndSongID(w, r)
	if !ok {
		return
	}
	if err := h.svc.Like(r.Context(), userID, songID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "like failed")
		return
	}
	w.WriteHeader(http.StatusOK)
}

// Unlike handles POST /api/queue/songs/{id}/unliked (protected).
func (h *Handler) Unlike(w http.ResponseWriter, r *http.Request) {
	userID, songID, ok := userAndSongID(w, r)
	if !ok {
		return
	}
	if err := h.svc.Unlike(r.Context(), userID, songID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "unlike failed")
		return
	}
	w.WriteHeader(http.StatusOK)
}

// --- helpers ---

func decodeUserAndSongReq(w http.ResponseWriter, r *http.Request) (uuid.UUID, SongIDRequest, bool) {
	userID, ok := httpx.GetUserID(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "not authenticated")
		return uuid.Nil, SongIDRequest{}, false
	}
	var req SongIDRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid json body")
		return uuid.Nil, SongIDRequest{}, false
	}
	return userID, req, true
}

func userAndSongID(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	userID, ok := httpx.GetUserID(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "not authenticated")
		return uuid.Nil, uuid.Nil, false
	}
	songID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid song id")
		return uuid.Nil, uuid.Nil, false
	}
	return userID, songID, true
}
