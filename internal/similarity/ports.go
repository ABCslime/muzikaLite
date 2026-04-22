package similarity

import (
	"context"

	"github.com/google/uuid"
)

// SeedReader resolves a queue_songs row to the (title, artist)
// pair that anchors a similar-mode session. Implemented in
// cmd/muzika/main.go by an adapter over queue.Service so this
// package doesn't import queue.
//
// Returns the zero Seed (with SongID, UserID set) if the row is
// missing or has no usable metadata — the caller decides how to
// surface that to the user (typically: "couldn't find this on
// Discogs — try another seed").
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
