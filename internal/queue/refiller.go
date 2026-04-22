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

// PreferredGenres is the refiller's lookup hook into the preferences
// package. Returns the user's preferred Bandcamp tags and Discogs genres.
// Empty slices mean "no preference" — the refiller falls back to the
// default tag/genre configured at startup.
//
// Living as a function type (not an interface) avoids a cross-domain
// import from queue → preferences. main.go wires an adapter that closes
// over *preferences.Service.
type PreferredGenres func(ctx context.Context, userID uuid.UUID) (bandcampTags, discogsGenres []string)

// SimilarModeFunc is the v0.5 PR B refiller hook into the similarity
// package, generalized in v0.6 PR D to return the user's multi-seed
// set instead of a single id. Empty slice = similar mode is off.
//
// When the slice is non-empty, the refiller picks ONE seed at
// random per refill cycle (not per trigger — a single Trigger
// emits `needed` intents, each with an independently sampled
// seed). Over many cycles the queue blends contributions from
// every seed in the user's set.
//
// Same function-type-not-interface rationale as PreferredGenres:
// avoids queue → similarity cross-import; main.go wires the adapter.
type SimilarModeFunc func(ctx context.Context, userID uuid.UUID) (seedSongIDs []uuid.UUID)

// Refiller keeps each user's queue topped up to minQueueSize by publishing
// DiscoveryIntent events (Strategy=StrategyRandom) when the queue is short.
// On-demand only — callers invoke Trigger after queue-mutating HTTP handlers;
// no background ticker.
//
// v0.4 PR 2: when Discogs is enabled, each emitted intent is routed either
// to Bandcamp or to Discogs by a weighted random pick on DiscogsWeight.
// Seeders read the chosen source off DiscoveryIntent.PreferredSources.
//
// v0.4.1 PR A: Genre is now picked per-intent. If the user has preferences
// for the chosen source, we pick a random tag/genre from their list;
// otherwise we fall back to the configured default genre. This lets a user
// follow "minimal house" and "vaporwave" as Bandcamp tags while keeping
// "Electronic" and "Rock" as Discogs genres.
//
// Weighting runs here (not in the seeders) because we want per-intent
// exclusivity — one DiscoveryIntent → one RequestDownload → one stub
// filled. A naive both-fanout approach would double the download rate
// and break the refiller's needed := minQueue - count arithmetic.
type Refiller struct {
	repo                 *Repo
	bus                  *bus.Bus
	minQueueSize         int
	defaultBandcampGenre string
	defaultDiscogsGenre  string
	log                  *slog.Logger
	discogsEnabled       bool
	discogsWeight        float64 // P(discogs) when enabled; 1 - P = P(bandcamp)
	prefs                PreferredGenres
	similarMode          SimilarModeFunc // nil = similar mode disabled (v0.5 PR B)

	mu  sync.Mutex
	rng *rand.Rand
}

// WithSimilarMode wires the v0.5 PR B similar-mode hook. nil
// (the default) leaves the refiller on its existing genre-random
// path. Returns the receiver for chaining at construction.
func (r *Refiller) WithSimilarMode(fn SimilarModeFunc) *Refiller {
	r.similarMode = fn
	return r
}

// NewRefiller constructs a Refiller with Bandcamp as the sole source and
// no per-user preferences. Retained for tests.
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
// defaultGenre serves as the fallback for both sources when the user has
// no preferences and no source-specific default is set via
// NewRefillerWithDefaults.
func NewRefillerWithDiscogs(
	repo *Repo,
	b *bus.Bus,
	minQueueSize int,
	defaultGenre string,
	log *slog.Logger,
	discogsEnabled bool,
	discogsWeight float64,
) *Refiller {
	return NewRefillerFull(repo, b, minQueueSize, defaultGenre, defaultGenre, log, discogsEnabled, discogsWeight, nil)
}

// NewRefillerFull is the v0.4.1 entry point: separate Bandcamp vs Discogs
// default genres, plus an optional PreferredGenres lookup hook.
//
// defaultBandcamp is used when the user has no Bandcamp-tag prefs (or
// when the routing picked Bandcamp); defaultDiscogs likewise for Discogs.
// prefs may be nil — queue-only deployments (tests, dev shims) don't need it.
func NewRefillerFull(
	repo *Repo,
	b *bus.Bus,
	minQueueSize int,
	defaultBandcamp, defaultDiscogs string,
	log *slog.Logger,
	discogsEnabled bool,
	discogsWeight float64,
	prefs PreferredGenres,
) *Refiller {
	return &Refiller{
		repo:                 repo,
		bus:                  b,
		minQueueSize:         minQueueSize,
		defaultBandcampGenre: defaultBandcamp,
		defaultDiscogsGenre:  defaultDiscogs,
		log:                  log,
		discogsEnabled:       discogsEnabled,
		discogsWeight:        discogsWeight,
		prefs:                prefs,
		//nolint:gosec // G404: source/genre pick is non-crypto, PCG/rand is fine
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

	// v0.5 PR B + v0.6 PR D: per-user similar mode. Looked up
	// ONCE per Trigger — same rationale as prefs. Empty slice =
	// off; the refiller takes the genre-random path for this
	// refill pass. Otherwise we pick a random seed PER STUB so
	// a single refill pass with multiple seeds blends them.
	var similarSeeds []uuid.UUID
	if r.similarMode != nil {
		similarSeeds = r.similarMode(ctx, userID)
	}
	similarOn := len(similarSeeds) > 0

	// Look up per-user preferences once per Trigger rather than per stub —
	// they don't change between stubs in the same refill pass. Skipped
	// when similar mode is on; the genre path won't be taken.
	var bandcampPrefs, discogsPrefs []string
	if !similarOn && r.prefs != nil {
		bandcampPrefs, discogsPrefs = r.prefs(ctx, userID)
	}

	for i := 0; i < needed; i++ {
		stubID := uuid.New()

		// requesting_user_id is stamped on the stub so onLoadedSong can
		// append to this specific user's queue when the download completes
		// — not to every user with a short queue.
		var stubGenre string
		if !similarOn {
			stubGenre = r.pickGenre(r.pickSource(), bandcampPrefs, discogsPrefs)
		}
		if err := r.repo.InsertSongStub(ctx, stubID, stubGenre, userID); err != nil {
			r.log.Error("refiller: insert stub", "err", err, "user_id", userID)
			continue
		}

		var ev bus.DiscoveryIntent
		if similarOn {
			// Random pick (not round-robin) across the seed set.
			// Avoids the queue feeling like a predictable cycle of
			// "seed1 pick, seed2 pick, seed3 pick, repeat" — random
			// mixes the buckets into a believable blend.
			r.mu.Lock()
			seed := similarSeeds[r.rng.Intn(len(similarSeeds))]
			r.mu.Unlock()
			ev = bus.DiscoveryIntent{
				SongID:     stubID,
				UserID:     userID,
				Strategy:   bus.StrategySimilarSong,
				SeedSongID: seed,
				// PreferredSources left nil — the similarity worker
				// is the only subscriber for similar_song intents.
			}
		} else {
			source := r.pickSource()
			ev = bus.DiscoveryIntent{
				SongID:           stubID,
				UserID:           userID,
				Strategy:         bus.StrategyRandom,
				Genre:            stubGenre,
				PreferredSources: source,
			}
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

// pickGenre selects the DiscoveryIntent.Genre string for this intent,
// respecting the chosen source and the user's preferences.
//
// Source resolution:
//   - source == nil or ["bandcamp"] → Bandcamp vocabulary
//   - source == ["discogs"]         → Discogs vocabulary
//
// Per-source vocabulary:
//   - If the user has prefs for this source: random pick from those.
//   - Otherwise: the source's default genre (MUZIKA_BANDCAMP_DEFAULT_TAGS[0]
//     or MUZIKA_DISCOGS_DEFAULT_GENRES[0], wired in main.go).
func (r *Refiller) pickGenre(source, bandcampPrefs, discogsPrefs []string) string {
	isDiscogs := len(source) == 1 && source[0] == "discogs"
	prefs := bandcampPrefs
	fallback := r.defaultBandcampGenre
	if isDiscogs {
		prefs = discogsPrefs
		fallback = r.defaultDiscogsGenre
	}
	if len(prefs) == 0 {
		return fallback
	}
	r.mu.Lock()
	idx := r.rng.Intn(len(prefs))
	r.mu.Unlock()
	return prefs[idx]
}
