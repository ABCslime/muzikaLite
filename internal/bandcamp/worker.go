package bandcamp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/bus"
	"github.com/macabc/muzika/internal/db"
)

// Service consumes RequestRandomSong events, asks the Client for a song
// matching the requested genre, and publishes RequestDownload events.
//
// On ErrNoResults (bandcamp returned no items for the tag) we emit a
// LoadedSong{Status: Error} via the outbox. queue's onLoadedSong handler
// deletes the orphaned stub; otherwise stubs accumulate forever whenever
// bandcamp has nothing matching the configured tags. The outbox path
// matches the download worker's failure semantics so there's one cleanup code path.
type Service struct {
	client     *Client
	db         *sql.DB
	bus        *bus.Bus
	dispatcher *bus.OutboxDispatcher
	log        *slog.Logger
}

// NewService wires a Service.
//
// sqlDB and dispatcher are required to emit LoadedSong{Error} via the outbox
// on ErrNoResults. They may be nil in tests that don't care about that path.
func NewService(c *Client, sqlDB *sql.DB, b *bus.Bus, d *bus.OutboxDispatcher) *Service {
	return &Service{
		client:     c,
		db:         sqlDB,
		bus:        b,
		dispatcher: d,
		log:        slog.Default().With("mod", "bandcamp"),
	}
}

// StartWorkers subscribes to RequestRandomSong with `workers` goroutines.
func (s *Service) StartWorkers(ctx context.Context, workers int) {
	ch := bus.Subscribe[bus.RequestRandomSong](s.bus, "bandcamp/request-random-song")
	bus.RunPool(ctx, s.bus, "bandcamp/request-random-song", workers, ch, s.onRequestRandomSong)
}

// OnRequestRandomSong is exported for tests.
func (s *Service) OnRequestRandomSong(ctx context.Context, ev bus.RequestRandomSong) error {
	return s.onRequestRandomSong(ctx, ev)
}

func (s *Service) onRequestRandomSong(ctx context.Context, ev bus.RequestRandomSong) error {
	result, err := s.client.Search(ctx, ev.Genre)
	if err != nil {
		if errors.Is(err, ErrNoResults) {
			s.log.Warn("bandcamp: no results; emitting LoadedSong error to clean up stub",
				"song_id", ev.SongID, "genre", ev.Genre)
			if emitErr := s.emitLoadedError(ctx, ev.SongID); emitErr != nil {
				// Best-effort — the refiller will re-observe the short queue.
				s.log.Error("bandcamp: emit LoadedSong error failed",
					"song_id", ev.SongID, "err", emitErr)
			}
			return nil
		}
		s.log.Error("bandcamp: search failed",
			"song_id", ev.SongID, "genre", ev.Genre, "err", err)
		return err
	}
	out := bus.RequestDownload{
		SongID: ev.SongID,
		Title:  result.Title,
		Artist: result.Artist,
	}
	if err := bus.Publish(ctx, s.bus, out, bus.PublishOpts{
		SendTimeout: 100 * time.Millisecond,
	}); err != nil {
		s.log.Warn("bandcamp: publish failed", "song_id", ev.SongID, "err", err)
	}
	return nil
}

// emitLoadedError writes a LoadedSong{Error} row to the outbox and wakes the
// dispatcher. Returns nil when DB/dispatcher are absent (tests).
func (s *Service) emitLoadedError(ctx context.Context, songID uuid.UUID) error {
	if s.db == nil {
		return nil
	}
	err := db.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		return bus.InsertOutboxTx(ctx, tx, bus.TypeLoadedSong, bus.LoadedSong{
			SongID: songID,
			Status: bus.LoadedStatusError,
		})
	})
	if err != nil {
		return fmt.Errorf("emit LoadedSong error: %w", err)
	}
	if s.dispatcher != nil {
		s.dispatcher.Wake()
	}
	return nil
}
