package playlist

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/bus"
)

// Service is the business entry-point for playlist operations.
type Service struct {
	db   *sql.DB
	repo *Repo
	bus  *bus.Bus
}

// NewService wires a Service.
func NewService(db *sql.DB, b *bus.Bus) *Service {
	return &Service{db: db, repo: NewRepo(db), bus: b}
}

// StartWorkers subscribes to UserCreated, UserDeleted, LikedSong, UnlikedSong.
// Called by main.go on boot.
// TODO(port): Phase 5.
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

func (s *Service) onUserCreated(ctx context.Context, ev bus.UserCreated) error { return nil }
func (s *Service) onUserDeleted(ctx context.Context, ev bus.UserDeleted) error { return nil }
func (s *Service) onLiked(ctx context.Context, ev bus.LikedSong) error         { return nil }
func (s *Service) onUnliked(ctx context.Context, ev bus.UnlikedSong) error     { return nil }

// ListForUser returns all playlists owned by userID. TODO(port): Phase 5.
func (s *Service) ListForUser(ctx context.Context, userID uuid.UUID) ([]PlaylistResponse, error) {
	return nil, errors.New("playlist.Service.ListForUser: not implemented")
}

// Get returns one playlist with its songs. TODO(port): Phase 5.
func (s *Service) Get(ctx context.Context, userID, playlistID uuid.UUID) (PlaylistWithSongsResponse, error) {
	return PlaylistWithSongsResponse{}, errors.New("playlist.Service.Get: not implemented")
}

// Create inserts a new playlist owned by userID. TODO(port): Phase 5.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, req CreatePlaylistRequest) (PlaylistResponse, error) {
	return PlaylistResponse{}, errors.New("playlist.Service.Create: not implemented")
}

// Delete removes a playlist if owned by userID. TODO(port): Phase 5.
func (s *Service) Delete(ctx context.Context, userID, playlistID uuid.UUID) error {
	return errors.New("playlist.Service.Delete: not implemented")
}

// AddSong appends a song to a playlist at the next position. TODO(port): Phase 5.
func (s *Service) AddSong(ctx context.Context, userID, playlistID, songID uuid.UUID) error {
	return errors.New("playlist.Service.AddSong: not implemented")
}

// RemoveSong removes a song from a playlist. TODO(port): Phase 5.
func (s *Service) RemoveSong(ctx context.Context, userID, playlistID, songID uuid.UUID) error {
	return errors.New("playlist.Service.RemoveSong: not implemented")
}
