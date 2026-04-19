// Package playlist manages user playlists and their songs. Reacts to
// UserCreated (creates the system-liked playlist), LikedSong, UnlikedSong,
// and UserDeleted (cache invalidation only; FK cascade handles rows).
//
// Phase 3 scaffold: interfaces and stubs only. Phase 5 ports business logic.
package playlist

import (
	"time"

	"github.com/google/uuid"
)

type Playlist struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	Name          string
	Description   string
	IsSystemLiked bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type PlaylistSong struct {
	PlaylistID uuid.UUID
	SongID     uuid.UUID
	Position   int
	AddedAt    time.Time
}

type CreatePlaylistRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type PlaylistResponse struct {
	ID          uuid.UUID `json:"id"`
	UserID      uuid.UUID `json:"userId"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt,omitempty"`
}

type PlaylistSongResponse struct {
	SongID   uuid.UUID `json:"songId"`
	Position int       `json:"position"`
	AddedAt  time.Time `json:"addedAt"`
}

type PlaylistWithSongsResponse struct {
	Playlist PlaylistResponse       `json:"playlist"`
	Songs    []PlaylistSongResponse `json:"songs"`
}
