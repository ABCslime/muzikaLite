package discogs

import (
	"context"
	"sync"

	"github.com/macabc/muzika/internal/discogs"
	"github.com/macabc/muzika/internal/similarity"
)

// genreEraYearsWindow scopes "same era" for genre-based
// candidates. Matches the other era buckets — keeps the user's
// mental model consistent.
const genreEraYearsWindow = 3

// genrePerSearchLimit caps one genre's candidate pull. Broader
// than styles (a genre like "Electronic" returns 100+ pressings
// per era easily), but still capped at 50 to keep cache payloads
// manageable. Post-year-filter we typically end up with 10-30
// candidates per genre, which combined with the engine's top-K=20
// picker gives plenty of variety.
const genrePerSearchLimit = 50

// SameGenreEra is the broadest, weakest-confidence bucket
// (default weight 1): releases in the same Discogs broad genre
// ("Electronic", "Rock", "Hip Hop") within the era window. Low
// weight by design — a same-era Electronic release is barely
// more related to a Daft Punk seed than a random Electronic
// pick. Kept in so very obscure seeds with sparse artist/label
// coverage still get SOMETHING to refill with.
//
// Bails out when the seed has no genres OR no year.
type SameGenreEra struct{ client *discogs.Client }

func NewSameGenreEra(c *discogs.Client) *SameGenreEra { return &SameGenreEra{client: c} }

func (b *SameGenreEra) ID() string    { return "discogs.same_genre_era" }
func (b *SameGenreEra) Label() string { return "Same genre, similar era" }
func (b *SameGenreEra) Description() string {
	return "Releases in the same broad Discogs genre within a few years of the seed. Widest net, lowest default weight."
}
func (b *SameGenreEra) DefaultWeight() float64 { return 1.0 }

func (b *SameGenreEra) Candidates(ctx context.Context, seed similarity.Seed) ([]similarity.Candidate, error) {
	if b.client == nil || len(seed.Genres) == 0 || seed.Year <= 0 {
		return nil, nil
	}
	type part struct {
		genre string
		rs    []discogs.SearchResult
	}
	parts := make([]part, 0, len(seed.Genres))
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for _, g := range seed.Genres {
		wg.Add(1)
		go func(genre string) {
			defer wg.Done()
			rs, err := b.client.SearchByGenre(ctx, genre, genrePerSearchLimit)
			if err != nil {
				return
			}
			rs = withinYears(rs, seed.Year, genreEraYearsWindow)
			mu.Lock()
			parts = append(parts, part{genre: genre, rs: rs})
			mu.Unlock()
		}(g)
	}
	wg.Wait()

	out := make([]similarity.Candidate, 0, 32)
	for _, p := range parts {
		cands := releasesToCandidates(p.rs)
		for i := range cands {
			cands[i].Edge = map[string]any{
				"discogs_genre": p.genre,
				"seed_year":     seed.Year,
				"era_window":    genreEraYearsWindow,
			}
		}
		out = append(out, cands...)
	}
	return out, nil
}
