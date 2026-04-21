package bandcamp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/bus"
	"github.com/macabc/muzika/internal/db"
)

// Service consumes DiscoveryIntent events, asks the Client for a song
// matching the requested genre, and publishes RequestDownload events.
//
// Strategy filter: the bandcamp seeder handles StrategyRandom only. Other
// strategies (StrategyGenre, StrategySearch, StrategySimilarSong, ...) are
// ignored — a future seeder subscribes to the same DiscoveryIntent channel
// and handles those. This is the "seeders filter on Strategy" contract from
// ROADMAP v0.4.
//
// Source filter: v0.4 PR 2 added the Discogs seeder on the same
// DiscoveryIntent channel. The refiller (queue.Refiller.pickSource) routes
// each intent to exactly one seeder by writing a one-element
// PreferredSources list. Bandcamp silently ignores intents whose list is
// non-empty and excludes "bandcamp". Empty list = legacy (pre-PR-2) or
// Discogs-disabled path; bandcamp handles it as before.
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

// StartWorkers subscribes to DiscoveryIntent with `workers` goroutines.
func (s *Service) StartWorkers(ctx context.Context, workers int) {
	ch := bus.Subscribe[bus.DiscoveryIntent](s.bus, "bandcamp/discovery-intent")
	bus.RunPool(ctx, s.bus, "bandcamp/discovery-intent", workers, ch, s.onDiscoveryIntent)
}

// OnDiscoveryIntent is exported for tests.
func (s *Service) OnDiscoveryIntent(ctx context.Context, ev bus.DiscoveryIntent) error {
	return s.onDiscoveryIntent(ctx, ev)
}

func (s *Service) onDiscoveryIntent(ctx context.Context, ev bus.DiscoveryIntent) error {
	// Bandcamp only serves the random-refill strategy today. Ignore everything
	// else silently — another seeder subscribed to the same channel will
	// handle it. No error: the event isn't for us, it's not a failure.
	if ev.Strategy != bus.StrategyRandom {
		return nil
	}
	if !sourceAllowed(ev.PreferredSources, "bandcamp") {
		return nil
	}
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
		SongID:   ev.SongID,
		Title:    result.Title,
		Artist:   result.Artist,
		Strategy: ev.Strategy,
	}
	// RequestDownload has no SendTimeout — v0.4.1 PR C (option D2). A seeder
	// has already spent real work (Bandcamp API call + a discovery_log row)
	// picking this (title, artist). Dropping the event orphans a stub and
	// wastes the seed effort. Blocking here applies back-pressure up to the
	// refiller, which is bounded by its own minQueue-count arithmetic.
	if err := bus.Publish(ctx, s.bus, out, bus.PublishOpts{}); err != nil {
		s.log.Warn("bandcamp: publish failed", "song_id", ev.SongID, "err", err)
	}
	return nil
}

// sourceAllowed returns true if prefs is empty or contains want.
// Empty means "any seeder is fine"; a non-empty list restricts routing.
// Mirrors the discogs package's identical helper — kept duplicated (not
// extracted to bus) to avoid dragging bandcamp and discogs into a shared
// utility package for three lines.
func sourceAllowed(prefs []string, want string) bool {
	if len(prefs) == 0 {
		return true
	}
	for _, p := range prefs {
		if p == want {
			return true
		}
	}
	return false
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
