package similarity

import (
	"context"

	"github.com/google/uuid"
)

// SeedReader resolves a queue_songs row to a fully-hydrated Seed
// ready for the engine: title + artist from queue_songs, plus
// the optional Discogs-derived features (release/artist/label
// IDs, year, styles, genres, collaborators) used by the buckets.
//
// Implemented in cmd/muzika/main.go by an adapter over
// queue.Service + discogs.Client so this package doesn't import
// either. The adapter does the Discogs resolution; the engine
// stays I/O-free.
//
// Hydration policy: hard-fail only when the queue_songs row
// itself is missing. A successful read with no Discogs match
// returns a partial Seed (Title + Artist set, Discogs IDs zero)
// — buckets that need Discogs IDs bail out gracefully on zero,
// and the engine returns ErrSeedUnknown when no bucket produced
// anything.
type SeedReader interface {
	ReadSeed(ctx context.Context, userID, songID uuid.UUID) (Seed, error)
}

// SongAcquirer hands a chosen candidate off to the existing
// search-acquire path so it lands in the user's queue with the
// same probe + ladder + image_url plumbing as any other queued
// release. Implemented in cmd/muzika/main.go by an adapter over
// queue.Service.Search (PrePicked).
//
// Returns the inserted queue_songs UUID, same shape as the
// playlist module's AcquireForUser. The caller doesn't need it
// today (the download worker's onLoadedSong path appends to the
// user's queue automatically); kept for future call sites that
// may want to track which catalog row a similarity pick produced.
type SongAcquirer interface {
	AcquireForUser(ctx context.Context, userID uuid.UUID, title, artist, imageURL string) (uuid.UUID, error)
}

// QueueDeduper reports whether a (title, artist) pair already
// has a row in the user's queue (queue_entries joined with
// queue_songs). The engine consults it to drop candidates the
// user already has — keeps similar mode from queuing the same
// track twice. Implemented in cmd/muzika/main.go via
// queue.Repo.FindEntry which already does the case-insensitive
// match.
//
// Returns true on hit, false on miss. Errors collapse to false
// (we'd rather risk a duplicate than skip a real proposal).
type QueueDeduper interface {
	HasEntry(ctx context.Context, userID uuid.UUID, title, artist string) bool
}

// WeightStore reads the user's tuned per-bucket weights. Returns
// a map[bucketID]weight; missing bucket IDs fall back to the
// bucket's DefaultWeight() at the engine layer. nil map is fine
// ("user has tuned nothing yet — use defaults everywhere").
//
// v0.5 PR D wires a real implementation against the
// user_similarity_settings JSON column; PR A through C use a
// no-op implementation that always returns nil.
type WeightStore interface {
	WeightsFor(ctx context.Context, userID uuid.UUID) (map[string]float64, error)
}

// noopWeightStore is the WeightStore the Service uses when no
// real store is wired (PRs A–C of v0.5; tests). Returns nil so
// every bucket falls through to its DefaultWeight().
type noopWeightStore struct{}

// NewNoopWeightStore returns a WeightStore that always returns
// nil weights. Callers fall back to bucket defaults.
func NewNoopWeightStore() WeightStore { return noopWeightStore{} }

func (noopWeightStore) WeightsFor(_ context.Context, _ uuid.UUID) (map[string]float64, error) {
	return nil, nil
}

// GenreFilter is the v0.6.1 port into the preferences package:
// returns the user's currently pinned Discogs genres/styles
// that should filter similar-mode picks to. Empty slice = no
// filter; the engine keeps every candidate. Bandcamp tags are
// NOT returned here — only Discogs vocabulary participates in
// the filter (Discogs candidates can only be reliably matched
// against Discogs-side tags).
//
// Implemented in cmd/muzika/main.go by an adapter closing over
// preferences.Service. Errors in the adapter collapse to empty
// slice — better to queue unfiltered picks than to stall.
type GenreFilter interface {
	PinnedGenresFor(ctx context.Context, userID uuid.UUID) []string
}

// CandidateEnricher looks up the Discogs genres + styles for a
// release id. Populated by the engine's filter step on every
// candidate with a non-zero DiscogsReleaseID. Implementation
// closes over the cached Discogs client in main.go, so a
// stable seed + genre combo eventually runs entirely off the
// 30-day cache.
//
// Returns (nil, nil) when the id is unknown or genres are
// missing — the filter treats that as "unknown genre, let the
// candidate through" rather than silently dropping.
type CandidateEnricher interface {
	GenresFor(ctx context.Context, discogsReleaseID int) ([]string, error)
}

// noopGenreFilter is the default when no filter is wired
// (legacy callers; tests). Always returns empty.
type noopGenreFilter struct{}

// NewNoopGenreFilter returns a GenreFilter that always returns
// empty. Engine skips filtering entirely with this.
func NewNoopGenreFilter() GenreFilter { return noopGenreFilter{} }

func (noopGenreFilter) PinnedGenresFor(_ context.Context, _ uuid.UUID) []string {
	return nil
}
