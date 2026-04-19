package queue

import (
	"context"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/bus"
)

// Refiller publishes RequestRandomSong events when a user's queue drops below
// the configured minimum. On-demand only — called from handlers after queue
// mutations (skip, finish, GET /queue). No background ticker. See ARCHITECTURE.md §9.
type Refiller struct {
	repo         *Repo
	bus          *bus.Bus
	minQueueSize int
	defaultGenre string
}

// NewRefiller constructs a Refiller.
func NewRefiller(repo *Repo, b *bus.Bus, minQueueSize int, defaultGenre string) *Refiller {
	return &Refiller{repo: repo, bus: b, minQueueSize: minQueueSize, defaultGenre: defaultGenre}
}

// Trigger computes how short the queue is for userID and publishes that many
// RequestRandomSong events. Publisher side is fire-and-forget; back-pressure
// is the bus's send-timeout policy for request events.
// TODO(port): Phase 6.
func (r *Refiller) Trigger(ctx context.Context, userID uuid.UUID) {
	// intentionally empty in the scaffold
}
