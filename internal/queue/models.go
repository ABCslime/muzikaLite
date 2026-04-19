// Package queue owns per-user playback queues, the song catalog, listen
// statistics, audio byte serving, and the on-demand refiller. It is the
// central hub of the old system — every other module feeds it.
//
// Phase 3 scaffold: interfaces and stubs only. Phase 6 ports business logic.
package queue

import (
	"time"

	"github.com/google/uuid"
)

type Song struct {
	ID       uuid.UUID
	Title    string
	Artist   string
	Album    string
	Genre    string
	Duration int // seconds
	URL      string
}

type QueueEntry struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	SongID    uuid.UUID
	Position  int
	CreatedAt time.Time
}

type UserSong struct {
	UserID          uuid.UUID
	SongID          uuid.UUID
	ListenCount     int
	FirstListenedAt time.Time
	LastListenedAt  time.Time
	Liked           bool
	Skipped         bool
}

// SongDTO mirrors the old Spring response so the frontend works unchanged.
type SongDTO struct {
	ID       uuid.UUID `json:"id"`
	Title    string    `json:"title,omitempty"`
	Artist   string    `json:"artist,omitempty"`
	Album    string    `json:"album,omitempty"`
	Genre    string    `json:"genre,omitempty"`
	Duration int       `json:"duration,omitempty"`
}

type QueueResponse struct {
	Songs []SongDTO `json:"songs"`
}

type AddSongRequest struct {
	SongID   uuid.UUID `json:"songId"`
	Position int       `json:"position"`
}

type SongIDRequest struct {
	SongID       uuid.UUID `json:"songId"`
	QueueEntryID uuid.UUID `json:"queueEntryId,omitempty"`
}

type SongLikedResponse struct {
	Liked bool `json:"liked"`
}
