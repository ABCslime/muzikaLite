package playlist

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/bus"
)

// ErrInvalidName is returned when a playlist name is empty / whitespace.
var ErrInvalidName = errors.New("playlist: name is required")

// Service is the business entry-point for playlist operations.
type Service struct {
	db   *sql.DB
	repo *Repo
	bus  *bus.Bus
}

// NewService wires a Service.
func NewService(sqlDB *sql.DB, b *bus.Bus) *Service {
	return &Service{db: sqlDB, repo: NewRepo(sqlDB), bus: b}
}

// Repo exposes the underlying Repo for tests.
func (s *Service) Repo() *Repo { return s.repo }

// StartWorkers subscribes to UserCreated, UserDeleted, LikedSong, UnlikedSong.
func (s *Service) StartWorkers(ctx context.Context) {
	userCreated := bus.Subscribe[bus.UserCreated](s.bus, "playlist/user-created")
	userDeleted := bus.Subscribe[bus.UserDeleted](s.bus, "playlist/user-deleted")
	liked := bus.Subscribe[bus.LikedSong](s.bus, "playlist/liked")
	unliked := bus.Subscribe[bus.UnlikedSong](s.bus, "playlist/unliked")

	bus.RunPool(ctx, s.bus, "playlist/user-created", 1, userCreated, s.onUserCreated)
	bus.RunPool(ctx, s.bus, "playlist/user-deleted", 1, userDeleted, s.onUserDeleted)
	bus.RunPool(ctx, s.bus, "playlist/liked", 1, liked, s.onLiked)
	bus.RunPool(ctx, s.bus, "playlist/unliked", 1, unliked, s.onUnliked)
}

// OnUserCreated is exported for tests that want to drive the handler directly.
func (s *Service) OnUserCreated(ctx context.Context, ev bus.UserCreated) error {
	return s.onUserCreated(ctx, ev)
}

// OnLiked is exported for tests.
func (s *Service) OnLiked(ctx context.Context, ev bus.LikedSong) error {
	return s.onLiked(ctx, ev)
}

// OnUnliked is exported for tests.
func (s *Service) OnUnliked(ctx context.Context, ev bus.UnlikedSong) error {
	return s.onUnliked(ctx, ev)
}

func (s *Service) onUserCreated(ctx context.Context, ev bus.UserCreated) error {
	_, err := s.repo.GetOrCreateSystemLiked(ctx, ev.UserID)
	if err != nil {
		return fmt.Errorf("auto-create system-liked: %w", err)
	}
	return nil
}

func (s *Service) onUserDeleted(_ context.Context, _ bus.UserDeleted) error {
	// FK cascade removed all playlist_playlists + playlist_songs rows.
	//
	// TODO: Hook for future per-user cache invalidation (sync.Mutex maps in
	// queue, JWT token caches, etc.). Currently no-op because FK cascade
	// handles all row-level cleanup.
	return nil
}

func (s *Service) onLiked(ctx context.Context, ev bus.LikedSong) error {
	pl, err := s.repo.GetOrCreateSystemLiked(ctx, ev.UserID)
	if err != nil {
		return err
	}
	if err := s.repo.AddSong(ctx, pl.ID, ev.SongID); err != nil {
		if errors.Is(err, ErrDuplicate) {
			return nil // idempotent: already in Liked
		}
		return err
	}
	return nil
}

func (s *Service) onUnliked(ctx context.Context, ev bus.UnlikedSong) error {
	pl, err := s.repo.GetOrCreateSystemLiked(ctx, ev.UserID)
	if err != nil {
		return err
	}
	if err := s.repo.RemoveSong(ctx, pl.ID, ev.SongID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil // idempotent: not in Liked
		}
		return err
	}
	return nil
}

// --- HTTP-facing methods (handlers call these with userID from context) ---

// ListForUser returns every playlist owned by userID.
//
// Lazily creates the system-liked playlist for users whose UserCreated event
// pre-dated this module's deployment. Removes one class of ordering bug
// (user exists but never got a Liked because the UserCreated handler wasn't
// subscribed yet); costs one extra SELECT per list. Can be removed once we
// can guarantee all users have a Liked playlist via migration.
func (s *Service) ListForUser(ctx context.Context, userID uuid.UUID) ([]PlaylistResponse, error) {
	if _, err := s.repo.GetOrCreateSystemLiked(ctx, userID); err != nil {
		return nil, err
	}
	ps, err := s.repo.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]PlaylistResponse, 0, len(ps))
	for _, p := range ps {
		out = append(out, toResponse(p))
	}
	return out, nil
}

// Get returns one playlist with its songs. Enforces ownership.
//
// Cross-user access returns ErrNotFound (→ 404), not ErrForbidden (→ 403).
// A 403 confirms "this ID exists but you can't have it" and lets an attacker
// enumerate playlist IDs; 404 is indistinguishable from "no such playlist."
// Applies to every ownership check below that reads someone else's row.
func (s *Service) Get(ctx context.Context, userID, playlistID uuid.UUID) (PlaylistWithSongsResponse, error) {
	pl, err := s.repo.Get(ctx, playlistID)
	if err != nil {
		return PlaylistWithSongsResponse{}, err
	}
	if pl.UserID != userID {
		return PlaylistWithSongsResponse{}, ErrNotFound
	}
	songs, err := s.repo.Songs(ctx, playlistID)
	if err != nil {
		return PlaylistWithSongsResponse{}, err
	}
	resp := PlaylistWithSongsResponse{
		Playlist: toResponse(pl),
		Songs:    make([]PlaylistSongResponse, 0, len(songs)),
	}
	for _, sg := range songs {
		resp.Songs = append(resp.Songs, PlaylistSongResponse{
			SongID:   sg.SongID,
			Position: sg.Position,
			AddedAt:  sg.AddedAt,
		})
	}
	return resp, nil
}

// Create inserts a new playlist owned by userID.
//
// Stamps p.CreatedAt from the Go clock before the insert and passes it in
// explicitly — the response can be built from the in-memory value without a
// post-commit s.repo.Get(p.ID) round-trip. Same pattern as auth.Register.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, req CreatePlaylistRequest) (PlaylistResponse, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return PlaylistResponse{}, ErrInvalidName
	}
	if len(name) > 255 {
		return PlaylistResponse{}, ErrInvalidName
	}
	p := Playlist{
		ID:          uuid.New(),
		UserID:      userID,
		Name:        name,
		Description: req.Description,
		CreatedAt:   time.Now().UTC(),
	}
	if err := s.repo.Create(ctx, p); err != nil {
		return PlaylistResponse{}, err
	}
	return toResponse(p), nil
}

// Delete removes a playlist if userID owns it. System-liked cannot be deleted.
//
// Cross-user access → ErrNotFound (no enumeration). System-liked refusal stays
// ErrForbidden — that's an authorized caller hitting a legitimately protected
// resource, not an attacker probing for IDs.
func (s *Service) Delete(ctx context.Context, userID, playlistID uuid.UUID) error {
	pl, err := s.repo.Get(ctx, playlistID)
	if err != nil {
		return err
	}
	if pl.UserID != userID {
		return ErrNotFound
	}
	if pl.IsSystemLiked {
		return ErrForbidden
	}
	return s.repo.Delete(ctx, playlistID)
}

// AddSong appends a song to a playlist if userID owns it.
// Cross-user access → ErrNotFound (see Get comment for rationale).
func (s *Service) AddSong(ctx context.Context, userID, playlistID, songID uuid.UUID) error {
	pl, err := s.repo.Get(ctx, playlistID)
	if err != nil {
		return err
	}
	if pl.UserID != userID {
		return ErrNotFound
	}
	return s.repo.AddSong(ctx, playlistID, songID)
}

// RemoveSong removes a song from a playlist if userID owns it.
// Cross-user access → ErrNotFound (see Get comment for rationale).
func (s *Service) RemoveSong(ctx context.Context, userID, playlistID, songID uuid.UUID) error {
	pl, err := s.repo.Get(ctx, playlistID)
	if err != nil {
		return err
	}
	if pl.UserID != userID {
		return ErrNotFound
	}
	return s.repo.RemoveSong(ctx, playlistID, songID)
}

func toResponse(p Playlist) PlaylistResponse {
	return PlaylistResponse{
		ID:          p.ID,
		UserID:      p.UserID,
		Name:        p.Name,
		Description: p.Description,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
	}
}
