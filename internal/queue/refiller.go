package queue

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/bus"
)

// Refiller keeps each user's queue topped up to minQueueSize by publishing
// DiscoveryIntent events (Strategy=StrategyRandom) when the queue is short.
// On-demand only — callers invoke Trigger after queue-mutating HTTP handlers;
// no background ticker.
type Refiller struct {
	repo         *Repo
	bus          *bus.Bus
	minQueueSize int
	defaultGenre string
	log          *slog.Logger
}

// NewRefiller constructs a Refiller.
func NewRefiller(
	repo *Repo,
	b *bus.Bus,
	minQueueSize int,
	defaultGenre string,
	log *slog.Logger,
) *Refiller {
	return &Refiller{
		repo:         repo,
		bus:          b,
		minQueueSize: minQueueSize,
		defaultGenre: defaultGenre,
		log:          log,
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
			SongID:   stubID,
			UserID:   userID,
			Strategy: bus.StrategyRandom,
			Genre:    r.defaultGenre,
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
