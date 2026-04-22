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
// apply the optional genre filter, and pick one from the top K
// weighted-randomly.
//
// Pure-ish means: buckets do their own I/O (Discogs calls) via
// closures; the engine's only direct I/O is the optional genre-
// filter step that looks up each candidate's genres via the
// CandidateEnricher. Both are cheap enough against the 30-day
// Discogs cache to stay within the per-cycle latency budget.
type engine struct {
	buckets     []Bucket
	weights     WeightStore
	deduper     QueueDeduper
	genreFilter GenreFilter
	enricher    CandidateEnricher

	// rng is injectable so tests can pin the weighted pick to a
	// deterministic order. Defaults to a per-instance source so
	// production picks vary across processes.
	rng *rand.Rand
}

func newEngine(buckets []Bucket, weights WeightStore, deduper QueueDeduper, genreFilter GenreFilter, enricher CandidateEnricher, rng *rand.Rand) *engine {
	if genreFilter == nil {
		genreFilter = NewNoopGenreFilter()
	}
	return &engine{
		buckets:     buckets,
		weights:     weights,
		deduper:     deduper,
		genreFilter: genreFilter,
		enricher:    enricher,
		rng:         rng,
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

	// DiscogsReleaseID is the first non-zero id any contributing
	// bucket supplied. Used by the v0.6.1 genre filter to look
	// up this (artist, title)'s Discogs genres. Zero = at least
	// one contributor without a Discogs id; filter lets the
	// candidate through unchanged.
	DiscogsReleaseID int
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
						Title:            c.Title,
						Artist:           c.Artist,
						ImageURL:         c.ImageURL,
						DiscogsReleaseID: c.DiscogsReleaseID,
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
				// Same single-write rule for the release id: the
				// first bucket contributor sets it, subsequent
				// buckets confirming the same (artist, title)
				// don't overwrite. Buckets sourcing from non-
				// Discogs data (future plugins) emit zero; the
				// filter treats the merged candidate as filter-
				// exempt if every contributor was zero.
				if existing.DiscogsReleaseID == 0 && c.DiscogsReleaseID != 0 {
					existing.DiscogsReleaseID = c.DiscogsReleaseID
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

	// v0.6.1 — genre filter. When the user has pinned genres AND
	// the engine has a CandidateEnricher wired, keep only
	// candidates whose Discogs genres/styles intersect the pin
	// set. Candidates without a DiscogsReleaseID pass through
	// (we can't look them up; don't silently drop). Empty
	// filter-result = fall back to unfiltered so the queue
	// doesn't stall; the service logs a discovery_log degraded
	// row for that case.
	pinned := e.genreFilter.PinnedGenresFor(ctx, seed.UserID)
	if len(pinned) > 0 && e.enricher != nil {
		kept := filterByGenres(ctx, survivors, pinned, e.enricher)
		if len(kept) > 0 {
			survivors = kept
		}
		// else: keep the unfiltered survivors. The caller may
		// choose to log this fallback — engine doesn't emit its
		// own telemetry.
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

// filterByGenres is the v0.6.1 post-merge filter. Keeps a
// candidate iff its Discogs genres/styles intersect the pinned
// set case-insensitively. Candidates without a DiscogsReleaseID
// (rare — plugin buckets, /database/search rows missing an id)
// can't be looked up and pass through unchanged.
//
// Enricher errors per candidate are treated as "unknown genre,
// pass through" rather than "drop": better to risk an off-genre
// pick than to silently block similar mode when Discogs is
// having a bad day.
func filterByGenres(ctx context.Context, items []*scoredCandidate, pinned []string, enricher CandidateEnricher) []*scoredCandidate {
	if len(items) == 0 || len(pinned) == 0 || enricher == nil {
		return items
	}
	pinnedLower := make(map[string]struct{}, len(pinned))
	for _, p := range pinned {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" {
			pinnedLower[p] = struct{}{}
		}
	}
	if len(pinnedLower) == 0 {
		return items
	}
	kept := make([]*scoredCandidate, 0, len(items))
	for _, c := range items {
		// We don't currently carry DiscogsReleaseID on
		// scoredCandidate — the engine merge loses it when
		// multiple buckets contribute the same candidate.
		// First contributor's id is fine; the genre is a
		// property of the (artist, title), not the specific
		// pressing. Store it in the merged form.
		id := c.DiscogsReleaseID
		if id == 0 {
			kept = append(kept, c)
			continue
		}
		genres, err := enricher.GenresFor(ctx, id)
		if err != nil {
			kept = append(kept, c) // enricher failure = let it through
			continue
		}
		matched := false
		for _, g := range genres {
			if _, ok := pinnedLower[strings.ToLower(strings.TrimSpace(g))]; ok {
				matched = true
				break
			}
		}
		if matched {
			kept = append(kept, c)
		}
	}
	return kept
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
