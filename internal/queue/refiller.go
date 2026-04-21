package queue

import (
	"context"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/bus"
)

// Refiller keeps each user's queue topped up to minQueueSize by publishing
// DiscoveryIntent events (Strategy=StrategyRandom) when the queue is short.
// On-demand only — callers invoke Trigger after queue-mutating HTTP handlers;
// no background ticker.
//
// v0.4 PR 2: when Discogs is enabled, each emitted intent is routed either
// to Bandcamp or to Discogs by a weighted random pick on DiscogsWeight.
// Seeders read the chosen source off DiscoveryIntent.PreferredSources.
//
// Weighting runs here (not in the seeders) because we want per-intent
// exclusivity — one DiscoveryIntent → one RequestDownload → one stub
// filled. A naive both-fanout approach would double the download rate
// and break the refiller's needed := minQueue - count arithmetic.
type Refiller struct {
	repo           *Repo
	bus            *bus.Bus
	minQueueSize   int
	defaultGenre   string
	log            *slog.Logger
	discogsEnabled bool
	discogsWeight  float64 // P(discogs) when enabled; 1 - P = P(bandcamp)

	mu  sync.Mutex
	rng *rand.Rand
}

// NewRefiller constructs a Refiller with Bandcamp as the sole source.
// NewRefillerWithDiscogs adds weighted Discogs routing.
func NewRefiller(
	repo *Repo,
	b *bus.Bus,
	minQueueSize int,
	defaultGenre string,
	log *slog.Logger,
) *Refiller {
	return NewRefillerWithDiscogs(repo, b, minQueueSize, defaultGenre, log, false, 0)
}

// NewRefillerWithDiscogs constructs a Refiller that may route to Discogs.
// When discogsEnabled=false, every intent routes to Bandcamp (no
// PreferredSources field written). When enabled, each intent carries a
// one-element PreferredSources list that exactly one seeder accepts.
func NewRefillerWithDiscogs(
	repo *Repo,
	b *bus.Bus,
	minQueueSize int,
	defaultGenre string,
	log *slog.Logger,
	discogsEnabled bool,
	discogsWeight float64,
) *Refiller {
	return &Refiller{
		repo:           repo,
		bus:            b,
		minQueueSize:   minQueueSize,
		defaultGenre:   defaultGenre,
		log:            log,
		discogsEnabled: discogsEnabled,
		discogsWeight:  discogsWeight,
		//nolint:gosec // G404: source pick is non-crypto, PCG/rand is fine
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Trigger counts the user's queue and, if short, inserts song stubs and
// publishes DiscoveryIntent events (Strategy=StrategyRandom). Fire-and-forget;
// errors are logged.
func (r *Refiller) Trigger(ctx context.Context, userID uuid.UUID) {
	count, err := r.repo.CountEntries(ctx, userID)
	if err != nil {
		r.log.Error("refiller: count entries", "err", err, "user_id", userID)
		return
	}
	needed := r.minQueueSize - count
	if needed <= 0 {
		return
	}
	for i := 0; i < needed; i++ {
		stubID := uuid.New()
		// requesting_user_id is stamped on the stub so onLoadedSong can
		// append to this specific user's queue when the download completes
		// — not to every user with a short queue.
		if err := r.repo.InsertSongStub(ctx, stubID, r.defaultGenre, userID); err != nil {
			r.log.Error("refiller: insert stub", "err", err, "user_id", userID)
			continue
		}
		ev := bus.DiscoveryIntent{
			SongID:           stubID,
			UserID:           userID,
			Strategy:         bus.StrategyRandom,
			Genre:            r.defaultGenre,
			PreferredSources: r.pickSource(),
		}
		// Request events publish directly with a short timeout — if the
		// subscriber channel is full, the refiller will re-observe the short
		// queue on the next Trigger call and re-emit.
		err := bus.Publish(ctx, r.bus, ev, bus.PublishOpts{
			SendTimeout: 100 * time.Millisecond,
		})
		if err != nil {
			r.log.Warn("refiller: publish failed", "err", err, "user_id", userID)
		}
	}
}

// pickSource returns the single-source PreferredSources list for the next
// emitted intent, or nil when Discogs is disabled (empty PreferredSources
// means "any seeder is fine", and Bandcamp is the only subscriber then).
//
// With Discogs enabled, the random draw enforces per-intent exclusivity —
// the intent will be accepted by exactly one seeder.
func (r *Refiller) pickSource() []string {
	if !r.discogsEnabled {
		return nil
	}
	r.mu.Lock()
	pick := r.rng.Float64()
	r.mu.Unlock()
	if pick < r.discogsWeight {
		return []string{"discogs"}
	}
	return []string{"bandcamp"}
}
