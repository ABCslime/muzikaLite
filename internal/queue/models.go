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
	// ImageURL is a Discogs (or equivalent) cover-art URL that the
	// frontend can <img src=…> directly. Empty for songs added via
	// a path that can't infer cover art (Bandcamp tag refills,
	// manual stubs). v0.4.3.
	ImageURL string
}

type QueueEntry struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	SongID    uuid.UUID
	Position  int
	CreatedAt time.Time
	// Relaxed is true when this entry was acquired via the download worker's
	// relaxed-gate fallback AND the originating intent was user-initiated
	// (StrategySearch). Passive refill relaxations never land in this flag —
	// ROADMAP §v0.4 item 6 keeps that path silent.
	Relaxed bool
	// Status (v0.4.1 PR B) tracks the search availability lifecycle:
	//   "probing"   — seeder has set metadata, download worker hasn't
	//                 confirmed peers yet. UI disables play + spinner.
	//   "ready"     — file downloaded, playable.
	//   "not_found" — probe turned up zero peers. UI shows "not on
	//                 Soulseek"; user dismisses via DELETE.
	// Only StrategySearch entries ever enter probing/not_found. Passive
	// refill inserts straight at "ready".
	Status string
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
// Relaxed (v0.4 PR 3) lets the UI show "no high-quality matches; showing
// best available" when a user-initiated search landed via the relaxed gate.
// Status (v0.4.1 PR B) drives the search availability UX: "probing" shows
// a spinner, "not_found" shows "not on Soulseek", "ready" is the default
// playable state.
type SongDTO struct {
	ID       uuid.UUID `json:"id"`
	Title    string    `json:"title,omitempty"`
	Artist   string    `json:"artist,omitempty"`
	Album    string    `json:"album,omitempty"`
	Genre    string    `json:"genre,omitempty"`
	Duration int       `json:"duration,omitempty"`
	Relaxed  bool      `json:"relaxed,omitempty"`
	Status   string    `json:"status,omitempty"`
	// ImageURL is the Discogs cover-art URL populated at acquire
	// time (v0.4.3). Empty for songs without linked artwork; the
	// frontend falls back to a gradient placeholder.
	ImageURL string `json:"imageUrl,omitempty"`
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

// SearchRequest is the body for POST /api/queue/search.
//
// Two modes, discriminated by whether metadata is set:
//
//   - Auto-pick (v0.4 PR 3): only Query is set. The server normalizes,
//     inserts a stub, publishes DiscoveryIntent{StrategySearch} and lets
//     the Discogs seeder pick the best match. This path is retained as a
//     fallback but the frontend now prefers Acquire via the preview
//     dropdown.
//
//   - Pre-picked acquire (v0.4.1 PR C): Title + Artist are set, optionally
//     CatalogNumber. The server skips the seeder step entirely — inserts a
//     stub with the chosen metadata and publishes RequestDownload directly.
//     Query is optional and used only for the correlation notice the UI
//     shows (“searching for …”).
//
// Servers handle the legacy shape via h.svc.Search and the pre-picked
// shape via h.svc.SearchAcquire. See queue/handler.go for the router.
type SearchRequest struct {
	Query         string `json:"query,omitempty"`
	Title         string `json:"title,omitempty"`
	Artist        string `json:"artist,omitempty"`
	CatalogNumber string `json:"catalogNumber,omitempty"`
	// ImageURL is the cover-art URL the frontend already knows from
	// its preview response. Plumbed through to queue_songs so the
	// first paint of the queue row has artwork without re-querying
	// Discogs. v0.4.3.
	ImageURL string `json:"imageUrl,omitempty"`
}

// PrePicked is true when the request carries enough metadata to skip
// the seeder — Title and Artist, at minimum.
func (r SearchRequest) PrePicked() bool {
	return r.Title != "" && r.Artist != ""
}

// SearchResponse is returned synchronously after the stub is inserted.
// The queue will be populated asynchronously once the seeder + download
// ladder complete; clients poll /api/queue/queue to observe the result.
// SongID is the stub's UUID — clients can use it to identify the
// search-triggered entry when it appears in GetQueue output.
type SearchResponse struct {
	SongID uuid.UUID `json:"songId"`
	Query  string    `json:"query"` // the normalized form actually used
}
