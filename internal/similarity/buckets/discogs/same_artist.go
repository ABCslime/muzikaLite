package discogs

import (
	"context"

	"github.com/macabc/muzika/internal/discogs"
	"github.com/macabc/muzika/internal/similarity"
)

// SameArtist proposes other releases by the seed song's primary
// Discogs artist. Highest-confidence bucket (default weight 5):
// "more by the same artist" is the strongest single signal of
// "you'll like this" without audio analysis.
//
// Bails out gracefully when the seed didn't resolve to a Discogs
// artist (common for Bandcamp-only seeds) — returns empty so the
// engine tries other buckets and falls back to ErrSeedUnknown
// only if everything misses.
//
// Cache fully amortized via discogs.Client (30-day TTL on
// /artists/{id}/releases). After warm-up, this bucket is one
// SQLite read per refill cycle.
type SameArtist struct{ client *discogs.Client }

// NewSameArtist constructs the bucket. main.go skips registering
// it when discogs.Client is nil (no Discogs token configured).
func NewSameArtist(c *discogs.Client) *SameArtist { return &SameArtist{client: c} }

func (b *SameArtist) ID() string             { return "discogs.same_artist" }
func (b *SameArtist) Label() string          { return "Same artist" }
func (b *SameArtist) Description() string    { return "Other releases by the same artist as the seed song." }
func (b *SameArtist) DefaultWeight() float64 { return 5.0 }

func (b *SameArtist) Candidates(ctx context.Context, seed similarity.Seed) ([]similarity.Candidate, error) {
	if b.client == nil || seed.DiscogsArtistID <= 0 {
		return nil, nil
	}
	// 50 keeps a single artist from dominating the candidate pool
	// across many refill cycles AND lines up with the existing
	// ArtistReleases default cap. The engine's queue-dedup pass
	// will further trim to "ones the user doesn't already have."
	releases, err := b.client.ArtistReleases(ctx, seed.DiscogsArtistID, 50)
	if err != nil {
		return nil, err
	}
	cands := releasesToCandidates(releases)
	for i := range cands {
		cands[i].Edge = map[string]any{
			"discogs_artist_id": seed.DiscogsArtistID,
		}
	}
	return cands, nil
}
