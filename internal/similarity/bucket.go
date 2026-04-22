// Package similarity is the v0.5 "similar to song" feature: a
// continuous refill source for the queue, peer to the existing
// genre-random refill, that picks one related candidate per refill
// cycle from a registered set of Bucket implementations.
//
// The bucket abstraction is the public extension point. v0.5 ships
// five built-in Discogs-backed buckets; v0.6 will add filesystem
// plugin buckets ("mods" — drop a binary in ~/.muzika/buckets/);
// v0.7 will add event-graph buckets (artists who played the same
// festival, label-party lineups, etc.). The Service does not know
// what a bucket is; it only knows it can ask any registered Bucket
// for candidates and combine them with user-tunable weights.
//
// Module rule: similarity does NOT import queue, playlist, or any
// other domain package. Cross-domain integration goes through the
// port interfaces in ports.go, implemented by adapters in
// cmd/muzika/main.go (same pattern as playlist.AlbumExpander).
package similarity

import (
	"context"

	"github.com/google/uuid"
)

// Bucket is the public contract for a "discovery angle" — one
// algorithmic way of proposing candidates given a seed song. v0.5
// implementations all live in internal/similarity/buckets/discogs/
// and use cached Discogs data; v0.6 plugin buckets implement the
// same interface over JSON-RPC stdio. The engine treats them
// identically.
//
// Stability: this interface is the v0.5 architectural commitment.
// Adding optional behaviors should be done via additional small
// interfaces a Bucket may also implement (e.g. an Available(seed)
// probe later) rather than widening Bucket itself, so existing
// implementations don't break.
type Bucket interface {
	// ID is the stable, machine-readable bucket identifier. Slash-
	// namespaced by data source ("discogs.same_artist",
	// "events.same_festival"). Persisted as the key in the user's
	// bucket_weights JSON, so a rename here is a breaking change for
	// anyone with tuned weights — pick well.
	ID() string

	// Label is the human-friendly name shown in the Settings weight
	// slider list ("Same artist", "Same festival lineup").
	Label() string

	// Description is the one-sentence explanation under Label
	// ("Other releases by the seed song's artist."). Reserved for
	// settings-UI tooltips.
	Description() string

	// DefaultWeight is what the engine uses when the user hasn't
	// tuned this bucket's weight. By convention 0..10. Zero = bucket
	// disabled by default; user can opt in by raising it.
	DefaultWeight() float64

	// Candidates returns proposals for this seed. Each candidate
	// carries its own local Confidence (0..1) on top of the bucket's
	// user weight; the engine combines them as
	// final_score = userWeight * candidateConfidence (summed across
	// buckets that produced the same candidate).
	//
	// Empty slice + nil error is normal ("nothing for this seed").
	// A non-nil error is logged at debug and treated as empty — one
	// buggy bucket must not stall the refill cycle.
	Candidates(ctx context.Context, seed Seed) ([]Candidate, error)
}

// Seed is what a Bucket gets to work from. Buckets may use any
// subset of the fields; populated lazily by the engine's hydrate
// step (resolve seed → Discogs release → fill DiscogsArtistID etc.).
//
// SongID and UserID are always set. The rest are best-effort:
// Title and Artist come from queue_songs; Discogs IDs are 0 if the
// hydrate step couldn't resolve a Discogs match (e.g. Bandcamp-only
// release) — buckets that need them should bail out gracefully.
type Seed struct {
	SongID uuid.UUID
	UserID uuid.UUID

	Title  string
	Artist string

	DiscogsReleaseID int
	DiscogsArtistID  int
	DiscogsLabelID   int
	Year             int
	Styles           []string
	Genres           []string
	// Collaborators are extra-artist Discogs IDs from the seed's
	// release credits, excluding the primary artist. Used by the
	// "frequent collaborators" bucket. Empty when the release has
	// no extra credits or the seed didn't resolve to Discogs.
	Collaborators []int
}

// Candidate is one proposal coming out of a Bucket. The engine
// merges Candidates from all buckets by case-insensitive
// (Artist, Title) and sums weighted scores.
//
// Confidence is the bucket's own 0..1 quality signal. Most v0.5
// built-in buckets just emit 1.0 for everything — the bucket
// weight itself encodes the confidence. Plugin or event-based
// buckets may want finer-grained per-candidate scoring.
type Candidate struct {
	Title    string
	Artist   string
	ImageURL string

	Confidence float64

	// SourceBucket is filled by the engine from the producing
	// Bucket.ID() — buckets don't need to set this themselves.
	SourceBucket string

	// Edge is bucket-specific provenance metadata for the v0.7
	// graph view ({"festival":"Sónar","year":2003}). Optional;
	// nil is fine. Not used for ranking.
	Edge map[string]any
}
