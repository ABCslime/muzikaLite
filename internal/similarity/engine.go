package similarity

import (
	"context"
	"math/rand"
	"sort"
	"strings"
	"sync"
)

// engineTopK is the size of the candidate window from which the
// final pick is drawn (weighted-random). Top-1-always would make
// the queue feel deterministic ("you always get this Daft Punk
// track from a Daft Punk seed"). Top-K weighted-random gives
// "feels related but not predictable." Tunable later.
const engineTopK = 20

// engine is the pure-ranker half of the similarity service: given
// a hydrated seed, fan out to every registered bucket in parallel,
// merge the candidates by (artist, title) summing weight ×
// confidence, drop duplicates against the user's existing queue,
// and pick one from the top K weighted-randomly.
//
// Pure means: no I/O of its own. Buckets do I/O via their own
// Discogs client; the engine only orchestrates. Makes it cheap
// to test with mock buckets.
type engine struct {
	buckets []Bucket
	weights WeightStore
	deduper QueueDeduper

	// rng is injectable so tests can pin the weighted pick to a
	// deterministic order. Defaults to a per-instance source so
	// production picks vary across processes.
	rng *rand.Rand
}

func newEngine(buckets []Bucket, weights WeightStore, deduper QueueDeduper, rng *rand.Rand) *engine {
	return &engine{
		buckets: buckets,
		weights: weights,
		deduper: deduper,
		rng:     rng,
	}
}

// bucketContribution pairs a bucket id with the score weight it
// added to a candidate. The list on scoredCandidate preserves
// insertion order so the first bucket that surfaced a candidate
// is traceable (useful for v0.7 graph view edge ordering and for
// discovery_log forensics).
type bucketContribution struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
}

// scoredCandidate is the engine's internal aggregate: one
// (artist, title) row with the summed score across all buckets
// that proposed it, plus the per-bucket contributions and the
// edge metadata each bucket attached.
type scoredCandidate struct {
	Title    string
	Artist   string
	ImageURL string
	Score    float64
	Buckets  []bucketContribution
	Edges    []map[string]any
}

// pickStats gives the caller diagnostic visibility into a pick
// cycle without forcing every test to inspect internals. poolSize
// is the post-merge, pre-top-K candidate count AFTER the dedup
// filter — the meaningful "how many choices did we have?"
// number for the discovery_log's ResultCount column.
type pickStats struct {
	PoolSize int
}

// pick fans out to all buckets, merges, dedupes, and returns one
// candidate. Returns (zero, stats, false) when no bucket produced
// anything usable for this seed — caller may fall back to the
// genre-random refill path.
func (e *engine) pick(ctx context.Context, seed Seed) (scoredCandidate, pickStats, bool) {
	if len(e.buckets) == 0 {
		return scoredCandidate{}, pickStats{}, false
	}

	weightMap, _ := e.weights.WeightsFor(ctx, seed.UserID)

	// Fan out. Each bucket runs in its own goroutine; results
	// collected via a mutex-protected map. We don't use channels
	// here because the merge step naturally reduces into a map
	// keyed by (artist, title) — a channel would just be an
	// intermediate slice we'd then merge anyway.
	var (
		mu    sync.Mutex
		merge = make(map[string]*scoredCandidate, 64)
		wg    sync.WaitGroup
	)
	for _, b := range e.buckets {
		w := bucketWeight(b, weightMap)
		if w <= 0 {
			// User has dialed this bucket to zero (or set a
			// negative value, which we coerce). Skip the network
			// call entirely — saves Discogs/cache lookups.
			continue
		}
		wg.Add(1)
		go func(b Bucket, w float64) {
			defer wg.Done()
			cands, err := b.Candidates(ctx, seed)
			if err != nil || len(cands) == 0 {
				return
			}
			mu.Lock()
			for _, c := range cands {
				if c.Title == "" || c.Artist == "" {
					continue
				}
				if isSeedSelf(seed, c) {
					continue
				}
				key := candidateKey(c.Artist, c.Title)
				existing, ok := merge[key]
				if !ok {
					existing = &scoredCandidate{
						Title:    c.Title,
						Artist:   c.Artist,
						ImageURL: c.ImageURL,
					}
					merge[key] = existing
				}
				contribution := w * candidateConfidence(c)
				existing.Score += contribution
				existing.Buckets = append(existing.Buckets, bucketContribution{
					ID:    b.ID(),
					Score: contribution,
				})
				if c.Edge != nil {
					existing.Edges = append(existing.Edges, c.Edge)
				}
				// Prefer the first non-empty image we see — image
				// quality across Discogs sources is roughly
				// equivalent and re-overwriting just churns.
				if existing.ImageURL == "" && c.ImageURL != "" {
					existing.ImageURL = c.ImageURL
				}
			}
			mu.Unlock()
		}(b, w)
	}
	wg.Wait()

	if len(merge) == 0 {
		return scoredCandidate{}, pickStats{}, false
	}

	// Drop dupes already in the user's queue. We do this AFTER
	// the merge so we don't pay the dedup probe for candidates
	// that didn't survive scoring anyway.
	survivors := make([]*scoredCandidate, 0, len(merge))
	for _, c := range merge {
		if e.deduper != nil && e.deduper.HasEntry(ctx, seed.UserID, c.Title, c.Artist) {
			continue
		}
		survivors = append(survivors, c)
	}
	if len(survivors) == 0 {
		return scoredCandidate{}, pickStats{}, false
	}
	stats := pickStats{PoolSize: len(survivors)}

	// Sort by score desc; tie-break on artist+title for
	// deterministic ordering (matters for tests).
	sort.SliceStable(survivors, func(i, j int) bool {
		if survivors[i].Score != survivors[j].Score {
			return survivors[i].Score > survivors[j].Score
		}
		return candidateKey(survivors[i].Artist, survivors[i].Title) <
			candidateKey(survivors[j].Artist, survivors[j].Title)
	})

	top := survivors
	if len(top) > engineTopK {
		top = top[:engineTopK]
	}
	picked := weightedPick(top, e.rng)
	return *picked, stats, true
}

// bucketWeight resolves the user weight for a bucket: explicit
// user setting if present, otherwise the bucket's default. Negative
// values clamp to 0 (treated as "disabled").
func bucketWeight(b Bucket, weights map[string]float64) float64 {
	if w, ok := weights[b.ID()]; ok {
		if w < 0 {
			return 0
		}
		return w
	}
	return b.DefaultWeight()
}

// candidateConfidence is the per-candidate score the bucket
// supplied. Defaults to 1.0 if a bucket emits 0 — unset shouldn't
// silently zero-out the contribution. Negative coerced to 0.
func candidateConfidence(c Candidate) float64 {
	if c.Confidence <= 0 {
		return 1.0
	}
	return c.Confidence
}

// candidateKey is the dedup key: case-insensitive (artist, title).
// Mirrors discogs.fetchArtistOrLabelReleases and
// queue.Repo.FindSongForReuse.
func candidateKey(artist, title string) string {
	return strings.ToLower(strings.TrimSpace(artist)) + "\x00" +
		strings.ToLower(strings.TrimSpace(title))
}

// isSeedSelf returns true when a candidate matches the seed's own
// (title, artist). Buckets like same_artist will naturally surface
// the seed — drop it so we don't queue what the user is already
// listening to.
func isSeedSelf(seed Seed, c Candidate) bool {
	return candidateKey(seed.Artist, seed.Title) == candidateKey(c.Artist, c.Title)
}

// weightedPick picks one entry from `items` proportional to Score.
// Items with Score <= 0 still get a tiny chance via a uniform
// epsilon — keeps the pick from panicking if every entry happens
// to score zero (shouldn't happen in practice, but defensive).
//
// rng nil = uses the package default rand source.
func weightedPick(items []*scoredCandidate, rng *rand.Rand) *scoredCandidate {
	if len(items) == 0 {
		return nil
	}
	if len(items) == 1 {
		return items[0]
	}

	total := 0.0
	for _, it := range items {
		s := it.Score
		if s <= 0 {
			s = 0.0001
		}
		total += s
	}
	var r float64
	if rng != nil {
		r = rng.Float64() * total
	} else {
		r = rand.Float64() * total //nolint:gosec // not crypto
	}
	cum := 0.0
	for _, it := range items {
		s := it.Score
		if s <= 0 {
			s = 0.0001
		}
		cum += s
		if r <= cum {
			return it
		}
	}
	// Floating-point drift safety net.
	return items[len(items)-1]
}
