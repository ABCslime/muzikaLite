package bus

import "github.com/google/uuid"

// Event type names used as outbox discriminators. Must match the Go type names
// exactly so (*Bus).dispatchByType can round-trip.
const (
	TypeUserCreated       = "UserCreated"
	TypeUserDeleted       = "UserDeleted"
	TypeLikedSong         = "LikedSong"
	TypeUnlikedSong       = "UnlikedSong"
	TypeLoadedSong        = "LoadedSong"
	TypeRequestRandomSong = "RequestRandomSong"
	TypeRequestSlskdSong  = "RequestSlskdSong"
)

// State-change events — delivered via the outbox. Subscribers MUST be idempotent.

type UserCreated struct {
	UserID   uuid.UUID `json:"user_id"`
	Username string    `json:"username"`
}

type UserDeleted struct {
	UserID uuid.UUID `json:"user_id"`
}

type LikedSong struct {
	UserID uuid.UUID `json:"user_id"`
	SongID uuid.UUID `json:"song_id"`
}

type UnlikedSong struct {
	UserID uuid.UUID `json:"user_id"`
	SongID uuid.UUID `json:"song_id"`
}

// LoadedSong is published by the Soulseek layer when a download completes or fails.
type LoadedSong struct {
	SongID   uuid.UUID    `json:"song_id"`
	FilePath string       `json:"file_path,omitempty"`
	Status   LoadedStatus `json:"status"`
}

type LoadedStatus string

const (
	LoadedStatusCompleted LoadedStatus = "completed"
	LoadedStatusError     LoadedStatus = "error"
)

// Request events — published directly (no outbox). Regenerable.

type RequestRandomSong struct {
	SongID uuid.UUID `json:"song_id"` // stub ID the caller has already inserted
	Genre  string    `json:"genre"`
}

type RequestSlskdSong struct {
	SongID uuid.UUID `json:"song_id"`
	Title  string    `json:"title"`
	Artist string    `json:"artist"`
}
