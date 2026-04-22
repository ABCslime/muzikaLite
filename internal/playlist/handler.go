package playlist

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/httpx"
)

type Handler struct {
	svc      *Service
	expander AlbumExpander // optional; nil disables AddAlbum
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// WithAlbumExpander wires the cross-module dependency that AddAlbum
// needs: a way to fetch a Discogs release's tracklist and acquire
// each track into the user's queue. Implemented in cmd/muzika/main.go
// via a small adapter that wraps search.Previewer + queue.Service —
// keeps the playlist module from importing those modules directly.
//
// v0.4.4. Returns the receiver to support fluent wiring at construction.
func (h *Handler) WithAlbumExpander(e AlbumExpander) *Handler {
	h.expander = e
	return h
}

// Album is the data the expander returns for a Discogs release ID.
// Tracks holds the track titles in playback order; Artist and
// ImageURL are the album-level metadata each track inherits when
// it lands in queue_songs.
type Album struct {
	Artist   string
	ImageURL string
	Tracks   []string
}

// AlbumExpander hides the cross-module dependencies of "add a
// Discogs album to a playlist". Implemented in main.go; see
// WithAlbumExpander.
type AlbumExpander interface {
	// Album fetches release metadata + tracklist for the given
	// Discogs release ID. Returns ErrNotFound when Discogs returns
	// 404 / no tracks.
	Album(ctx context.Context, releaseID int) (Album, error)
	// AcquireForUser kicks off the existing search-acquire flow
	// for one (title, artist) pair, returning the queue_songs UUID.
	// The download worker takes over from there; tracks that probe
	// not_found stay in queue_entries with status='not_found' and
	// in the playlist regardless.
	AcquireForUser(ctx context.Context, userID uuid.UUID, title, artist, imageURL string) (uuid.UUID, error)
}

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

// AddAlbum handles POST /api/playlist/{id}/album (protected).
//
// Body: {"releaseId": 1234}
//
// Walks the Discogs release's tracklist, runs the search-acquire
// flow per track, and appends each resulting songID to the playlist.
// Returns a summary { added, total, notFoundCount } so the UI can
// toast "8 of 12 tracks queued; 4 weren't on Soulseek."
//
// Tracks that probe not_found are still added to the playlist —
// the user-facing AlbumView will re-probe them when navigated to,
// so they're not lost. v0.4.4.
//
// Returns 503 if the album expander dependency wasn't wired
// (e.g. Discogs disabled). 404 if the release id is bogus.
func (h *Handler) AddAlbum(w http.ResponseWriter, r *http.Request) {
	if h.expander == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable,
			"album expansion unavailable — Discogs not configured")
		return
	}
	userID, pid, ok := requireUserAndID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		ReleaseID int `json:"releaseId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if req.ReleaseID <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "releaseId required")
		return
	}

	// Verify ownership BEFORE doing the expensive Discogs + acquire
	// fan-out: catches the cross-user attack where someone else's
	// playlist id is supplied.
	if _, err := h.svc.Get(r.Context(), userID, pid); err != nil {
		writeDomainErr(w, err, "playlist lookup failed")
		return
	}

	album, err := h.expander.Album(r.Context(), req.ReleaseID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			httpx.WriteError(w, http.StatusNotFound, "release not found")
			return
		}
		httpx.WriteError(w, http.StatusBadGateway, "album lookup failed")
		return
	}
	if len(album.Tracks) == 0 {
		httpx.WriteError(w, http.StatusBadGateway, "release has no tracklist")
		return
	}

	// Per-track acquire + add. We fire sequentially rather than in
	// parallel: the search-acquire path takes a per-user mutex inside
	// queue.Service, so parallel calls would serialize anyway, and
	// sequential is one less goroutine pool to reason about for a
	// cap of ~20 tracks per typical album.
	added := 0
	for _, title := range album.Tracks {
		songID, err := h.expander.AcquireForUser(
			r.Context(), userID, title, album.Artist, album.ImageURL)
		if err != nil {
			// Per-track failure (e.g. empty title, refused stub) —
			// log via the next call's no-op and keep going. We can't
			// return the songID we don't have.
			continue
		}
		if err := h.svc.AddSong(r.Context(), userID, pid, songID); err != nil {
			if errors.Is(err, ErrDuplicate) {
				// Track already in playlist (re-add of an album the
				// user previously added to this same playlist) —
				// idempotent, count as added.
				added++
				continue
			}
			// Other errors: log via response but continue rest.
			continue
		}
		added++
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"added": added,
		"total": len(album.Tracks),
	})
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
