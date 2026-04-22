package similarity

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/bus"
	"github.com/macabc/muzika/internal/discovery"
)

// ErrSeedUnknown is returned by NextPick when the seed song
// can't be resolved to anything Discogs (or whatever the seed
// reader's backend is) recognizes — and therefore no bucket
// can produce candidates. The refiller treats this as "fall
// back to genre-random for this cycle"; the frontend may
// surface it as a third lens-button visual state.
var ErrSeedUnknown = errors.New("similarity: seed has no Discogs match")

// ErrNoCandidates is returned when the engine ran but every
// bucket returned empty (or every candidate got filtered out
// by the queue-dedup step). Indistinguishable from ErrSeedUnknown
// from the user's POV — the refiller falls back the same way.
var ErrNoCandidates = errors.New("similarity: no candidates after merge + dedup")

// Service is the entry point external callers (refiller,
// HTTP handler) hit. Owns the bucket registry, the engine, and
// the ports adapters that bridge into queue.
//
// Subscribes to DiscoveryIntent{StrategySimilarSong} for
// observability — every intent gets a discovery_log row even
// if NextPick was called directly. v0.5 PR A wires the
// subscription as a no-op logger; PR B routes intents into
// the actual NextPick flow.
type Service struct {
	seedReader   SeedReader
	songAcquirer SongAcquirer
	weights      WeightStore
	deduper      QueueDeduper
	bus          *bus.Bus
	logw         *discovery.Writer
	log          *slog.Logger

	mu      sync.RWMutex
	buckets []Bucket
	engine  *engine // rebuilt when buckets change (rare; startup-only today)

	rng *rand.Rand
}

// Config wires Service dependencies. logw may be nil to skip
// discovery_log writes (tests). bus may be nil to disable the
// DiscoveryIntent subscriber (also tests).
type Config struct {
	SeedReader   SeedReader
	SongAcquirer SongAcquirer
	Weights      WeightStore
	Deduper      QueueDeduper
	Bus          *bus.Bus
	Discovery    *discovery.Writer
}

// NewService constructs a Service with no buckets registered.
// Call Register one or more times before StartWorkers, then
// the refiller can pull NextPick.
//
// A Service with zero buckets is valid: NextPick returns
// ErrNoCandidates and the refiller falls back to genre-random.
// This makes PR A's empty-state ship cleanly even before any
// buckets exist.
func NewService(cfg Config) *Service {
	weights := cfg.Weights
	if weights == nil {
		weights = NewNoopWeightStore()
	}
	s := &Service{
		seedReader:   cfg.SeedReader,
		songAcquirer: cfg.SongAcquirer,
		weights:      weights,
		deduper:      cfg.Deduper,
		bus:          cfg.Bus,
		logw:         cfg.Discovery,
		log:          slog.Default().With("mod", "similarity"),
		rng:          rand.New(rand.NewSource(time.Now().UnixNano())), //nolint:gosec
	}
	s.rebuildEngine()
	return s
}

// Register adds a bucket to the registry. Safe to call before
// or after StartWorkers; rebuilds the engine each time. v0.5
// expects all Register calls at startup (cmd/muzika/main.go);
// future plugin hot-reload would make Register the natural API.
func (s *Service) Register(b Bucket) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buckets = append(s.buckets, b)
	s.rebuildEngine()
}

// Buckets returns the current registry as a snapshot. Used by
// the v0.5 PR D settings UI to render one slider per registered
// bucket.
func (s *Service) Buckets() []Bucket {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Bucket, len(s.buckets))
	copy(out, s.buckets)
	return out
}

// rebuildEngine snapshots the current bucket list into a fresh
// engine. The mutex is held by the caller (Register / NewService).
func (s *Service) rebuildEngine() {
	bucketsCopy := make([]Bucket, len(s.buckets))
	copy(bucketsCopy, s.buckets)
	s.engine = newEngine(bucketsCopy, s.weights, s.deduper, s.rng)
}

// StartWorkers subscribes the service to bus events. v0.5 PR A
// wires only the discovery-intent observer (logs each intent +
// no-ops); PR B will branch on Strategy=StrategySimilarSong and
// drive NextPick + acquire from the consumed event. Idempotent:
// safe to call zero times in tests that hit NextPick directly.
func (s *Service) StartWorkers(ctx context.Context) {
	if s.bus == nil {
		return
	}
	intents := bus.Subscribe[bus.DiscoveryIntent](s.bus, "similarity/discovery-intent")
	bus.RunPool(ctx, s.bus, "similarity/discovery-intent", 1, intents, s.onDiscoveryIntent)
}

// onDiscoveryIntent is the bus subscriber. Filters on
// Strategy=StrategySimilarSong; everything else returns nil
// silently (the bandcamp + discogs seeders handle the other
// strategies — same fan-out pattern as the rest of the system).
//
// v0.5 PR A: logs and no-ops. PR B: drives NextPick + acquire.
func (s *Service) onDiscoveryIntent(_ context.Context, ev bus.DiscoveryIntent) error {
	if ev.Strategy != bus.StrategySimilarSong {
		return nil
	}
	s.log.Debug("similarity: received intent (no-op until PR B)",
		"user_id", ev.UserID, "seed_song_id", ev.SeedSongID)
	return nil
}

// NextPick is the refiller-facing entry point: given the user's
// active similar-mode seed song, return one (title, artist,
// imageURL) ready to hand off to SongAcquirer.
//
// Errors:
//   - ErrSeedUnknown: seed metadata couldn't be resolved (no
//     Discogs match, missing row). Refiller falls back to
//     genre-random.
//   - ErrNoCandidates: engine ran but every bucket returned
//     nothing usable. Same fallback.
//   - other: log + same fallback. The refiller never blocks
//     on a similarity miss.
//
// v0.5 PR A: returns ErrNoCandidates always (no buckets
// registered). PR B wires the first two buckets and this
// becomes a real path.
func (s *Service) NextPick(ctx context.Context, userID, seedSongID uuid.UUID) (Candidate, error) {
	if s.seedReader == nil {
		return Candidate{}, fmt.Errorf("similarity: seed reader not wired")
	}
	seed, err := s.seedReader.ReadSeed(ctx, userID, seedSongID)
	if err != nil {
		return Candidate{}, fmt.Errorf("similarity: read seed: %w", err)
	}
	if seed.Title == "" || seed.Artist == "" {
		s.recordSeedUnknown(ctx, seed)
		return Candidate{}, ErrSeedUnknown
	}

	s.mu.RLock()
	eng := s.engine
	s.mu.RUnlock()

	picked, ok := eng.pick(ctx, seed)
	if !ok {
		s.recordNoCandidates(ctx, seed)
		return Candidate{}, ErrNoCandidates
	}

	s.recordPicked(ctx, seed, picked)
	return Candidate{
		Title:        picked.Title,
		Artist:       picked.Artist,
		ImageURL:     picked.ImageURL,
		Confidence:   picked.Score,
		SourceBucket: pickedTopBucket(picked),
	}, nil
}

// pickedTopBucket returns the highest-frequency contributing
// bucket — used as SourceBucket on the returned Candidate.
// Ties broken by which bucket appeared first.
func pickedTopBucket(c scoredCandidate) string {
	if len(c.Buckets) == 0 {
		return ""
	}
	counts := make(map[string]int, len(c.Buckets))
	for _, b := range c.Buckets {
		counts[b]++
	}
	best := c.Buckets[0]
	bestN := counts[best]
	for _, b := range c.Buckets[1:] {
		if counts[b] > bestN {
			best = b
			bestN = counts[b]
		}
	}
	return best
}

// --- discovery_log helpers ---

func (s *Service) recordSeedUnknown(ctx context.Context, seed Seed) {
	if s.logw == nil {
		return
	}
	s.logw.Record(ctx, discovery.Record{
		SongID:   seed.SongID,
		UserID:   seed.UserID,
		Source:   discovery.SourceDiscogs,
		Strategy: string(bus.StrategySimilarSong),
		Stage:    discovery.StageSeed,
		Outcome:  discovery.OutcomeNoResults,
		Rung:     -1,
		Reason:   "seed has no resolvable metadata",
	})
}

func (s *Service) recordNoCandidates(ctx context.Context, seed Seed) {
	if s.logw == nil {
		return
	}
	s.logw.Record(ctx, discovery.Record{
		SongID:   seed.SongID,
		UserID:   seed.UserID,
		Source:   discovery.SourceDiscogs,
		Strategy: string(bus.StrategySimilarSong),
		Stage:    discovery.StageSeed,
		Outcome:  discovery.OutcomeNoResults,
		Rung:     -1,
		Reason:   "buckets returned no candidates after dedup",
	})
}

func (s *Service) recordPicked(ctx context.Context, seed Seed, picked scoredCandidate) {
	if s.logw == nil {
		return
	}
	s.logw.Record(ctx, discovery.Record{
		SongID:   seed.SongID,
		UserID:   seed.UserID,
		Source:   discovery.SourceDiscogs,
		Strategy: string(bus.StrategySimilarSong),
		Stage:    discovery.StageSeed,
		Outcome:  discovery.OutcomeOK,
		Rung:     -1,
		Reason: fmt.Sprintf("picked %s — %s (score=%.2f, buckets=%v)",
			picked.Artist, picked.Title, picked.Score, picked.Buckets),
	})
}
