package playlist

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
)

var (
	ErrNotFound  = errors.New("playlist: not found")
	ErrForbidden = errors.New("playlist: not owner")
	ErrDuplicate = errors.New("playlist: duplicate")
)

// Repo persists playlists and their song lists.
type Repo struct{ db *sql.DB }

// NewRepo constructs a Repo.
func NewRepo(db *sql.DB) *Repo { return &Repo{db: db} }

// ListByUser returns every playlist owned by userID. TODO(port): Phase 5.
func (r *Repo) ListByUser(ctx context.Context, userID uuid.UUID) ([]Playlist, error) {
	return nil, errors.New("playlist.Repo.ListByUser: not implemented")
}

// Get returns a playlist if owned by userID, else ErrForbidden / ErrNotFound.
// TODO(port): Phase 5.
func (r *Repo) Get(ctx context.Context, userID, playlistID uuid.UUID) (Playlist, error) {
	return Playlist{}, ErrNotFound
}

// Create inserts a playlist. TODO(port): Phase 5.
func (r *Repo) Create(ctx context.Context, p Playlist) error {
	return errors.New("playlist.Repo.Create: not implemented")
}

// Delete removes a playlist (and cascades to playlist_songs). TODO(port): Phase 5.
func (r *Repo) Delete(ctx context.Context, userID, playlistID uuid.UUID) error {
	return errors.New("playlist.Repo.Delete: not implemented")
}

// AddSong inserts a row in playlist_songs. TODO(port): Phase 5.
func (r *Repo) AddSong(ctx context.Context, playlistID, songID uuid.UUID) error {
	return errors.New("playlist.Repo.AddSong: not implemented")
}

// RemoveSong deletes a playlist_songs row. TODO(port): Phase 5.
func (r *Repo) RemoveSong(ctx context.Context, playlistID, songID uuid.UUID) error {
	return errors.New("playlist.Repo.RemoveSong: not implemented")
}

// GetOrCreateSystemLiked returns the user's system-liked playlist, creating
// it if absent. Uses the partial-unique index uniq_system_liked_per_user.
// TODO(port): Phase 5.
func (r *Repo) GetOrCreateSystemLiked(ctx context.Context, userID uuid.UUID) (Playlist, error) {
	return Playlist{}, errors.New("playlist.Repo.GetOrCreateSystemLiked: not implemented")
}

// Songs returns songs in a playlist ordered by position. TODO(port): Phase 5.
func (r *Repo) Songs(ctx context.Context, playlistID uuid.UUID) ([]PlaylistSong, error) {
	return nil, errors.New("playlist.Repo.Songs: not implemented")
}
