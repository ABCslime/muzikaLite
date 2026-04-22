package similarity

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
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

	// v0.5 PR E: per-user last-error message so the frontend can
	// render a "similar mode is on but not working" state on the
	// lens icon (orange instead of pink). In-memory; lost across
	// restarts, which is fine — the next refill cycle re-discovers
	// the state in ~seconds. Cleared on successful NextPick or
	// on SetSeed (user explicitly changing the seed).
	muErrs   sync.RWMutex
	lastErrs map[uuid.UUID]string
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
		lastErrs:     make(map[uuid.UUID]string),
	}
	s.rebuildEngine()
	return s
}

// LastError returns the most recent NextPick failure reason for
// this user, or "" if the last cycle succeeded (or no cycle has
// run yet). The handler exposes this via GET /api/queue/similar-
// mode so the frontend can render the lens in its "active but
// couldn't resolve" orange state.
func (s *Service) LastError(userID uuid.UUID) string {
	s.muErrs.RLock()
	defer s.muErrs.RUnlock()
	return s.lastErrs[userID]
}

// ReadSeedMetadata returns the (title, artist) of a seed song,
// exposing the SeedReader adapter so the HTTP handler can render
// the Home-view chip without a second DB round-trip and without
// reaching into the adapter directly.
//
// Returns only the display-facing subset of Seed — we intentionally
// don't leak the Discogs hydration fields (those are internal to
// the engine). v0.5 PR F.
func (s *Service) ReadSeedMetadata(ctx context.Context, userID, seedSongID uuid.UUID) (Seed, error) {
	if s.seedReader == nil {
		return Seed{}, fmt.Errorf("similarity: seed reader not wired")
	}
	return s.seedReader.ReadSeed(ctx, userID, seedSongID)
}

// ClearLastError drops the cached error for a user. Called by
// the handler on SetSeed so a new seed starts with a clean slate
// (stale error from the previous seed wouldn't be meaningful).
func (s *Service) ClearLastError(userID uuid.UUID) {
	s.muErrs.Lock()
	defer s.muErrs.Unlock()
	delete(s.lastErrs, userID)
}

// setLastError is the internal write path used by NextPick. "" msg
// clears the entry. Keeping the API surface narrow (no public
// SetLastError) so only the engine's own result path updates it.
func (s *Service) setLastError(userID uuid.UUID, msg string) {
	s.muErrs.Lock()
	defer s.muErrs.Unlock()
	if msg == "" {
		delete(s.lastErrs, userID)
		return
	}
	s.lastErrs[userID] = msg
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
// Mirrors the discogs seeder's onDiscoveryIntent shape: a stub
// has already been inserted by the refiller and lives at
// ev.SongID. Our job is to pick a (title, artist) and publish
// RequestDownload back at the same SongID. The download worker
// + queue.onRequestDownload handle the rest exactly as for any
// other seeded intent.
//
// On ErrNoCandidates / ErrSeedUnknown we publish a
// LoadedSong{Error} so queue.onLoadedSong reaps the orphaned
// stub — same cleanup contract bandcamp + discogs already use
// for "this seeder couldn't pick anything."
func (s *Service) onDiscoveryIntent(ctx context.Context, ev bus.DiscoveryIntent) error {
	if ev.Strategy != bus.StrategySimilarSong {
		return nil
	}
	if ev.SeedSongID == uuid.Nil {
		return s.cleanupOrphanedStub(ctx, ev.SongID, "similar intent missing seed_song_id")
	}

	picked, err := s.NextPick(ctx, ev.UserID, ev.SeedSongID)
	if err != nil {
		// ErrSeedUnknown / ErrNoCandidates: not exceptional. Just
		// reap the stub and let the refiller observe-short-queue +
		// re-trigger on the next cycle. The user's frontend may
		// eventually flip the lens off in a future PR; for now the
		// next click on the lens with a different seed recovers.
		s.log.Debug("similarity: pick failed",
			"err", err, "seed_song_id", ev.SeedSongID, "user_id", ev.UserID)
		return s.cleanupOrphanedStub(ctx, ev.SongID, err.Error())
	}

	out := bus.RequestDownload{
		SongID:   ev.SongID,
		Title:    picked.Title,
		Artist:   picked.Artist,
		Strategy: bus.StrategySimilarSong,
		ImageURL: picked.ImageURL,
	}
	if err := bus.Publish(ctx, s.bus, out, bus.PublishOpts{}); err != nil {
		s.log.Warn("similarity: publish RequestDownload failed",
			"err", err, "song_id", ev.SongID)
		return err
	}
	return nil
}

// cleanupOrphanedStub publishes a LoadedSong{Error} so queue's
// onLoadedSong handler deletes the unfilled stub. Mirrors the
// failure path in discogs/worker.go (emitLoadedError) without
// importing it. We keep this private; only the on-intent failure
// path needs it.
func (s *Service) cleanupOrphanedStub(ctx context.Context, songID uuid.UUID, reason string) error {
	if s.bus == nil {
		return nil
	}
	s.log.Debug("similarity: reaping orphaned stub",
		"song_id", songID, "reason", reason)
	ev := bus.LoadedSong{
		SongID: songID,
		Status: bus.LoadedStatusError,
	}
	// Direct publish (not via outbox): we don't have a tx in scope
	// here, and the Error status only triggers a delete — at-most-
	// once is fine for cleanup. If the publish itself fails we'll
	// have one orphaned row in queue_songs; the refiller's existing
	// "count entries" arithmetic still works because orphans aren't
	// in queue_entries.
	if err := bus.Publish(ctx, s.bus, ev, bus.PublishOpts{}); err != nil {
		s.log.Warn("similarity: cleanup publish failed",
			"err", err, "song_id", songID)
	}
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
		s.setLastError(userID, "couldn't read seed song")
		return Candidate{}, fmt.Errorf("similarity: read seed: %w", err)
	}
	if seed.Title == "" || seed.Artist == "" {
		s.recordSeedUnknown(ctx, seed)
		s.setLastError(userID, "seed has no Discogs match")
		return Candidate{}, ErrSeedUnknown
	}

	s.mu.RLock()
	eng := s.engine
	s.mu.RUnlock()

	picked, stats, ok := eng.pick(ctx, seed)
	if !ok {
		s.recordNoCandidates(ctx, seed)
		s.setLastError(userID, "no candidates after bucket merge + queue dedup")
		return Candidate{}, ErrNoCandidates
	}

	s.recordPicked(ctx, seed, picked, stats)
	s.setLastError(userID, "") // clear on success
	return Candidate{
		Title:        picked.Title,
		Artist:       picked.Artist,
		ImageURL:     picked.ImageURL,
		Confidence:   picked.Score,
		SourceBucket: pickedTopBucket(picked),
	}, nil
}

// pickedTopBucket returns the highest-contributing bucket — used
// as SourceBucket on the returned Candidate. Highest score wins;
// ties broken by first-seen order.
func pickedTopBucket(c scoredCandidate) string {
	if len(c.Buckets) == 0 {
		return ""
	}
	best := c.Buckets[0]
	for _, b := range c.Buckets[1:] {
		if b.Score > best.Score {
			best = b
		}
	}
	return best.ID
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

func (s *Service) recordPicked(ctx context.Context, seed Seed, picked scoredCandidate, stats pickStats) {
	if s.logw == nil {
		return
	}
	// Breakdown: "same_artist:5.00, same_label_era:3.00". Lets a
	// later analytics query answer "per user, which bucket is
	// actually earning its configured weight?" — the full
	// contribution table, not just the winner.
	parts := make([]string, 0, len(picked.Buckets))
	for _, b := range picked.Buckets {
		parts = append(parts, fmt.Sprintf("%s:%.2f", b.ID, b.Score))
	}
	s.logw.Record(ctx, discovery.Record{
		SongID:      seed.SongID,
		UserID:      seed.UserID,
		Source:      discovery.SourceDiscogs,
		Strategy:    string(bus.StrategySimilarSong),
		Stage:       discovery.StageSeed,
		Outcome:     discovery.OutcomeOK,
		Rung:        -1,
		ResultCount: stats.PoolSize,
		Reason: fmt.Sprintf("picked %s — %s (score=%.2f, buckets=[%s])",
			picked.Artist, picked.Title, picked.Score,
			strings.Join(parts, ", ")),
	})
}
