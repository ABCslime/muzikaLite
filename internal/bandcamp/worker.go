package bandcamp

import (
	"context"

	"github.com/macabc/muzika/internal/bus"
)

// Service is the bandcamp module's runtime entry-point. It consumes
// RequestRandomSong, calls Search, and publishes RequestSlskdSong (directly,
// not via outbox — request events are regenerable).
type Service struct {
	client *Client
	bus    *bus.Bus
}

// NewService wires a Service.
func NewService(c *Client, b *bus.Bus) *Service {
	return &Service{client: c, bus: b}
}

// StartWorkers subscribes to RequestRandomSong with `workers` goroutines.
// TODO(port): Phase 7.
func (s *Service) StartWorkers(ctx context.Context, workers int) {
	ch := bus.Subscribe[bus.RequestRandomSong](s.bus, "bandcamp/request-random-song")
	bus.RunPool(ctx, s.bus, "bandcamp/request-random-song", workers, ch, s.onRequestRandomSong)
}

func (s *Service) onRequestRandomSong(ctx context.Context, ev bus.RequestRandomSong) error {
	return nil
}
