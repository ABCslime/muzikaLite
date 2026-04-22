package discogs

import (
	"context"

	"github.com/macabc/muzika/internal/discogs"
	"github.com/macabc/muzika/internal/similarity"
)

// labelEraYearsWindow scopes "same era" to ±N years around the
// seed's year. 3 is the ROADMAP §v0.5 default — narrow enough to
// keep the recommendations tonally close, wide enough to catch
// a label's natural release rhythm (most labels release across
// 1-3 year clusters per artist).
const labelEraYearsWindow = 3

// SameLabelEra proposes other releases on the seed's primary
// Discogs label, filtered to seedYear ± labelEraYearsWindow.
// Genre-tight labels (Warp, Ed Banger, Ghostly) make this a
// surprisingly strong signal; mainstream labels (Universal,
// Sony) make it noisier — the user's bucket-weight slider in
// PR D will let them dial it down for those cases.
//
// Bails out gracefully when the seed has no Discogs label OR no
// year — both are required to bucket meaningfully.
type SameLabelEra struct{ client *discogs.Client }

// NewSameLabelEra constructs the bucket. main.go skips
// registration when discogs.Client is nil.
func NewSameLabelEra(c *discogs.Client) *SameLabelEra { return &SameLabelEra{client: c} }

func (b *SameLabelEra) ID() string    { return "discogs.same_label_era" }
func (b *SameLabelEra) Label() string { return "Same label, similar era" }
func (b *SameLabelEra) Description() string {
	return "Other releases on the same label within a few years of the seed."
}
func (b *SameLabelEra) DefaultWeight() float64 { return 3.0 }

func (b *SameLabelEra) Candidates(ctx context.Context, seed similarity.Seed) ([]similarity.Candidate, error) {
	if b.client == nil || seed.DiscogsLabelID <= 0 || seed.Year <= 0 {
		return nil, nil
	}
	// 100 (vs. 50 for same_artist) because labels often have far
	// more releases than any one artist — pulling 100 then
	// year-windowing down typically leaves 20-50 candidates,
	// which is the sweet spot for the engine's top-K=20 pick.
	releases, err := b.client.LabelReleases(ctx, seed.DiscogsLabelID, 100)
	if err != nil {
		return nil, err
	}
	releases = withinYears(releases, seed.Year, labelEraYearsWindow)
	cands := releasesToCandidates(releases)
	for i := range cands {
		cands[i].Edge = map[string]any{
			"discogs_label_id": seed.DiscogsLabelID,
			"seed_year":        seed.Year,
			"era_window":       labelEraYearsWindow,
		}
	}
	return cands, nil
}
