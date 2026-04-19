// Package slskd is a thin wrapper around internal/soulseek.Client. It owns
// the RequestSlskdSong worker pool: search via the Soulseek backend, pick a
// peer, download the file, and emit LoadedSong (via outbox).
//
// The backend implementation (slskd daemon vs. gosk) is selected in main.go
// and passed in here as a soulseek.Client interface.
//
// Phase 3 scaffold: interfaces and stubs only. Phase 8 ports business logic.
package slskd

import (
	"context"
	"database/sql"

	"github.com/macabc/muzika/internal/bus"
	"github.com/macabc/muzika/internal/soulseek"
)

// Service owns the RequestSlskdSong worker pool.
type Service struct {
	db               *sql.DB
	soulseek         soulseek.Client
	bus              *bus.Bus
	dispatcher       *bus.OutboxDispatcher
	musicStoragePath string
}

// NewService wires a Service. `sk` is the backend-agnostic Soulseek client.
func NewService(
	db *sql.DB,
	sk soulseek.Client,
	musicStoragePath string,
	b *bus.Bus,
	d *bus.OutboxDispatcher,
) *Service {
	return &Service{
		db:               db,
		soulseek:         sk,
		bus:              b,
		dispatcher:       d,
		musicStoragePath: musicStoragePath,
	}
}

// StartWorkers subscribes to RequestSlskdSong with `workers` goroutines.
// TODO(port): Phase 8.
func (s *Service) StartWorkers(ctx context.Context, workers int) {
	ch := bus.Subscribe[bus.RequestSlskdSong](s.bus, "slskd/request-slskd-song")
	bus.RunPool(ctx, s.bus, "slskd/request-slskd-song", workers, ch, s.onRequestSlskdSong)
}

func (s *Service) onRequestSlskdSong(ctx context.Context, ev bus.RequestSlskdSong) error {
	// Planned flow (Phase 8):
	//   1. s.soulseek.Search(ctx, query, window)
	//   2. pick peer (filter by FilesShared, QueueLen)
	//   3. s.soulseek.Download(...)
	//   4. poll s.soulseek.DownloadStatus until completed/failed
	//   5. in a tx, insert LoadedSong outbox row with filePath; commit;
	//      s.dispatcher.Wake() so the dispatcher drains immediately.
	return nil
}
