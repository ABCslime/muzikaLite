package discogs

import (
	"context"
	"sync"

	"github.com/macabc/muzika/internal/discogs"
	"github.com/macabc/muzika/internal/similarity"
)

// collaboratorsCap bounds the per-cycle fan-out. A single release
// can credit 20+ extra artists (remixers, session players, A&R,
// design); chasing all of them per cycle would wastefully warm
// the cache with mostly-irrelevant artist discographies. 3 keeps
// the cost predictable and still surfaces the creative core (lead
// vocalist, producer, featured rapper for pop/rap seeds).
//
// Mitigation for the cap: each cycle of similar mode re-runs the
// engine, and Discogs returns extraartists in insertion order —
// which tends to prioritize higher-role credits — so across N
// cycles we meaningfully sample the collaborator network.
const collaboratorsCap = 3

// collaboratorReleasesLimit caps one collaborator's release
// pull. 30 (vs. 50 for same_artist) because collaborators are
// supporting signal — we don't want any single collaborator's
// deep catalog to drown out the primary artist's bucket on the
// merge.
const collaboratorReleasesLimit = 30

// Collaborators proposes releases by artists credited alongside
// the seed's primary artist (vocalists, remixers, producers on
// the seed's specific release). Medium-weight bucket (default 2):
// a credited collaborator shares an aesthetic context with the
// seed without being the seed's primary artist — exactly the
// "adjacent but not same" signal that makes discovery feel alive
// rather than same-artist-looping.
//
// Bails out when the seed has no collaborators (parseReleaseDetail
// sets this from extraartists[]; empty for releases without
// extra credits).
type Collaborators struct{ client *discogs.Client }

func NewCollaborators(c *discogs.Client) *Collaborators { return &Collaborators{client: c} }

func (b *Collaborators) ID() string    { return "discogs.collaborators" }
func (b *Collaborators) Label() string { return "Frequent collaborators" }
func (b *Collaborators) Description() string {
	return "Releases by other artists credited on the seed's release (featured vocalists, remixers, producers)."
}
func (b *Collaborators) DefaultWeight() float64 { return 2.0 }

func (b *Collaborators) Candidates(ctx context.Context, seed similarity.Seed) ([]similarity.Candidate, error) {
	if b.client == nil || len(seed.Collaborators) == 0 {
		return nil, nil
	}
	ids := seed.Collaborators
	if len(ids) > collaboratorsCap {
		ids = ids[:collaboratorsCap]
	}

	type part struct {
		id int
		rs []discogs.SearchResult
	}
	parts := make([]part, 0, len(ids))
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for _, id := range ids {
		wg.Add(1)
		go func(aid int) {
			defer wg.Done()
			rs, err := b.client.ArtistReleases(ctx, aid, collaboratorReleasesLimit)
			if err != nil {
				return
			}
			mu.Lock()
			parts = append(parts, part{id: aid, rs: rs})
			mu.Unlock()
		}(id)
	}
	wg.Wait()

	out := make([]similarity.Candidate, 0, 32)
	for _, p := range parts {
		cands := releasesToCandidates(p.rs)
		for i := range cands {
			cands[i].Edge = map[string]any{
				"collaborator_discogs_id": p.id,
			}
		}
		out = append(out, cands...)
	}
	return out, nil
}
