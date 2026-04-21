package bus

import "github.com/google/uuid"

// Event type names used as outbox discriminators. Must match the Go type names
// exactly so (*Bus).dispatchByType can round-trip.
const (
	TypeUserCreated     = "UserCreated"
	TypeUserDeleted     = "UserDeleted"
	TypeLikedSong       = "LikedSong"
	TypeUnlikedSong     = "UnlikedSong"
	TypeLoadedSong      = "LoadedSong"
	TypeDiscoveryIntent = "DiscoveryIntent"
	TypeRequestDownload = "RequestDownload"

	// TypeRequestRandomSong is the pre-v0.4 name for what is now
	// TypeDiscoveryIntent. Kept as a deprecated alias so the outbox dispatcher
	// can decode any straggler rows left over from pre-v0.4 deployments; the
	// legacy payload shape ({song_id, user_id, genre}) unmarshals cleanly into
	// DiscoveryIntent, and the dispatcher backfills Strategy=StrategyRandom
	// before republishing. No current publisher uses this constant — do not
	// reintroduce one.
	//
	// Deprecated: use TypeDiscoveryIntent.
	TypeRequestRandomSong = "RequestRandomSong"
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
//
// Relaxed=true means the file was acquired only after the download gate
// fell back to relaxed thresholds (halved floors / doubled ceilings). v0.4
// PR 3 surfaces this to the queue handler so user-initiated search can show
// "no high-quality matches; showing best available" in the UI. For passive
// refill, onLoadedSong discards the flag (ROADMAP §v0.4 item 6 — passive
// relaxes silently).
type LoadedSong struct {
	SongID   uuid.UUID    `json:"song_id"`
	FilePath string       `json:"file_path,omitempty"`
	Status   LoadedStatus `json:"status"`
	Relaxed  bool         `json:"relaxed,omitempty"`
}

type LoadedStatus string

const (
	LoadedStatusCompleted LoadedStatus = "completed"
	LoadedStatusError     LoadedStatus = "error"

	// LoadedStatusNotFound is the v0.4.1 PR B signal that a user-initiated
	// search turned up zero Soulseek peers for the Discogs-picked track.
	// Distinct from LoadedStatusError so the UI can toast "not found on
	// Soulseek" specifically. queue.onLoadedSong deletes any queue_entries
	// row with status='probing' for this stub; the UI notices it
	// disappearing between polls and surfaces the toast.
	LoadedStatusNotFound LoadedStatus = "not_found"
)

// Request events — published directly (no outbox). Regenerable.

// Strategy names the kind of discovery that produced a DiscoveryIntent.
// Seeders filter on Strategy to decide whether to handle a given intent; the
// set is closed — unknown values are ignored by every seeder.
//
// StrategyRandom, StrategyGenre, and StrategySearch are implemented in v0.4.
// StrategySimilarSong and StrategySimilarPlaylist are declared here so the
// event shape is stable across v0.4 → v0.5, but no publisher emits them yet
// and no subscriber handles them.
type Strategy string

const (
	// StrategyRandom is the refiller's passive top-up. Genre is the tag hint;
	// Query, SeedSongID, and SeedPlaylistID are unset.
	StrategyRandom Strategy = "random"

	// StrategyGenre is a user-initiated "more like this genre" request.
	// Genre is required. Reserved for v0.4.1's user preferences path.
	StrategyGenre Strategy = "genre"

	// StrategySearch is a user-typed search query. Query is required; Genre
	// and seed IDs are unset. Reserved for v0.4 PR 3.
	StrategySearch Strategy = "search"

	// StrategySimilarSong is "find songs like this one". SeedSongID is
	// required. Reserved for v0.5; no current handler.
	StrategySimilarSong Strategy = "similar_song"

	// StrategySimilarPlaylist is "find songs like this playlist".
	// SeedPlaylistID is required. Reserved for v0.5.2; no current handler.
	StrategySimilarPlaylist Strategy = "similar_playlist"
)

// DiscoveryIntent is the canonical "please find a song" event. It supersedes
// the pre-v0.4 RequestRandomSong event, widening the shape so one event type
// carries every discovery path (passive refill, genre-bias, user search,
// similarity). Seeders subscribe once and filter on Strategy and
// PreferredSources. Adding a new strategy is one enum value + one seeder,
// never a new event type.
//
// Wire compat: the JSON fields song_id, user_id, and genre retained the same
// names as the legacy event so old outbox payloads (if any ever landed there
// during pre-v0.4 migration windows) decode cleanly. See outbox.go for the
// dispatchByType backward-compat case on TypeRequestRandomSong.
type DiscoveryIntent struct {
	// SongID is the stub queue_songs row the caller has already inserted.
	// Seeders fill in title/artist via RequestDownload; the queue service
	// reaps the stub if discovery fails.
	SongID uuid.UUID `json:"song_id"`

	// UserID is the requester. Carried so downstream workers (e.g. per-user
	// metrics, v0.5 similarity against the user's listen history) can avoid
	// a DB hit to re-read it off the stub.
	UserID uuid.UUID `json:"user_id"`

	// Strategy determines which seeder handles this intent. See the Strategy
	// constants above.
	Strategy Strategy `json:"strategy"`

	// Genre is the tag hint. Populated for StrategyRandom (refiller's
	// configured default) and StrategyGenre. Empty otherwise.
	Genre string `json:"genre,omitempty"`

	// Query is the user-typed search string. Populated for StrategySearch
	// only.
	Query string `json:"query,omitempty"`

	// SeedSongID is the similarity anchor for StrategySimilarSong.
	// uuid.Nil otherwise.
	SeedSongID uuid.UUID `json:"seed_song_id,omitempty"`

	// SeedPlaylistID is the similarity anchor for StrategySimilarPlaylist.
	// uuid.Nil otherwise.
	SeedPlaylistID uuid.UUID `json:"seed_playlist_id,omitempty"`

	// PreferredSources optionally restricts which seeder(s) should handle
	// this intent. Empty means any seeder subscribed to the matching
	// Strategy is fine. Values are source names — today "bandcamp" or
	// "discogs"; future seeders add their own.
	PreferredSources []string `json:"preferred_sources,omitempty"`
}

// RequestDownload is emitted by a seeder when it picks a (title, artist) pair,
// and consumed by the download worker pool (and by queue, for a metadata
// sync-up). The event name reflects the intent — "please obtain this track" —
// rather than which backend ultimately fetches it.
//
// CatalogNumber is populated by seeders that have one (Discogs) and left
// empty by seeders that don't (Bandcamp). The download ladder (ROADMAP §v0.4
// item 4) uses it as rung 0 of the search fallthrough — catno → artist+title
// → title. Rung 0 is skipped when the field is empty.
//
// Strategy (v0.4 PR 3) carries the originating DiscoveryIntent.Strategy so
// the download worker can decide whether to surface relax-mode. Empty
// Strategy means "unknown / legacy" and is treated as passive (silent)
// relax — kept permissive so a stale RequestDownload from before PR 3
// still works.
type RequestDownload struct {
	SongID        uuid.UUID `json:"song_id"`
	Title         string    `json:"title"`
	Artist        string    `json:"artist"`
	CatalogNumber string    `json:"catalog_number,omitempty"`
	Strategy      Strategy  `json:"strategy,omitempty"`
}
