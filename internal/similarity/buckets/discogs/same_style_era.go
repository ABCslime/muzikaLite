package discogs

import (
	"context"
	"sync"

	"github.com/macabc/muzika/internal/discogs"
	"github.com/macabc/muzika/internal/similarity"
)

// styleEraYearsWindow scopes "same era" for style-based
// candidates to ±N years. Matches the same_label_era window so
// users' mental model of "era" stays consistent across buckets.
const styleEraYearsWindow = 3

// stylePerSearchLimit caps one style's candidate pull. Discogs
// allows up to 100 per page; 50 balances "enough results to pick
// from after year-filtering" against "cache payload size." Styles
// like "House" return 50 easily; rare styles return fewer, no
// harm done.
const stylePerSearchLimit = 50

// SameStyleEra proposes releases sharing any of the seed's
// Discogs styles, filtered to seedYear ± styleEraYearsWindow.
// Styles are the sub-genre vocabulary ("Detroit Techno",
// "Vaporwave") — narrower than Discogs' broad genres. When the
// seed has multiple styles we fan out one search per style and
// merge — a release matching two styles accumulates two bucket
// contributions (via the engine's merge), naturally ranking it
// higher.
//
// Bails out when the seed has no styles (rare; most releases are
// tagged) OR no year (year window is the whole point). Skipped
// without Discogs configured.
type SameStyleEra struct{ client *discogs.Client }

func NewSameStyleEra(c *discogs.Client) *SameStyleEra { return &SameStyleEra{client: c} }

func (b *SameStyleEra) ID() string    { return "discogs.same_style_era" }
func (b *SameStyleEra) Label() string { return "Same style, similar era" }
func (b *SameStyleEra) Description() string {
	return "Releases tagged with the same Discogs sub-genre within a few years of the seed."
}
func (b *SameStyleEra) DefaultWeight() float64 { return 3.0 }

func (b *SameStyleEra) Candidates(ctx context.Context, seed similarity.Seed) ([]similarity.Candidate, error) {
	if b.client == nil || len(seed.Styles) == 0 || seed.Year <= 0 {
		return nil, nil
	}
	// Parallel fan-out over styles. Each style's search is
	// independently cached, so warm-path cost is near-zero; the
	// parallelism only matters on a cold first click.
	type bucketPart struct {
		style string
		rs    []discogs.SearchResult
	}
	parts := make([]bucketPart, 0, len(seed.Styles))
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for _, s := range seed.Styles {
		wg.Add(1)
		go func(style string) {
			defer wg.Done()
			rs, err := b.client.SearchByStyle(ctx, style, stylePerSearchLimit)
			if err != nil {
				return
			}
			rs = withinYears(rs, seed.Year, styleEraYearsWindow)
			mu.Lock()
			parts = append(parts, bucketPart{style: style, rs: rs})
			mu.Unlock()
		}(s)
	}
	wg.Wait()

	out := make([]similarity.Candidate, 0, 32)
	for _, p := range parts {
		cands := releasesToCandidates(p.rs)
		for i := range cands {
			cands[i].Edge = map[string]any{
				"discogs_style": p.style,
				"seed_year":     seed.Year,
				"era_window":    styleEraYearsWindow,
			}
		}
		out = append(out, cands...)
	}
	return out, nil
}
