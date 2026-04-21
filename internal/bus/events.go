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
	TypeRequestDownload   = "RequestDownload"
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
	UserID uuid.UUID `json:"user_id"` // requester — the stub row carries it too,
	//                                   but passing it in the event lets log
	//                                   context and future downstream workers
	//                                   (e.g. per-user metrics) avoid a DB hit.
	Genre string `json:"genre"`
}

// RequestDownload is emitted by bandcamp when it picks a (title, artist) pair,
// and consumed by the download worker pool (and by queue, for a metadata
// sync-up). The event name reflects the intent — "please obtain this track" —
// rather than which backend ultimately fetches it.
type RequestDownload struct {
	SongID uuid.UUID `json:"song_id"`
	Title  string    `json:"title"`
	Artist string    `json:"artist"`
}
