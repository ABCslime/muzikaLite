package discogs

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
	"github.com/macabc/muzika/internal/discovery"
)

// sourceName is the PreferredSources value that routes DiscoveryIntents to
// this seeder. Kept as a string literal in one place so the refiller's
// weighted pick and the worker's filter agree.
const sourceName = "discogs"

// Service consumes DiscoveryIntent events, queries Discogs, and publishes
// RequestDownload (with CatalogNumber populated).
//
// Strategy filter: StrategyRandom only in PR 2. Per ROADMAP, StrategyGenre
// is deferred to v0.4.1 (user preferences UI) and StrategySearch to PR 3.
//
// Source filter: if the incoming intent carries a non-empty PreferredSources
// list that doesn't include "discogs", the event is silently ignored —
// another seeder will handle it. The refiller's weighted pick uses this to
// route random intents 30% to Discogs and 70% to Bandcamp (defaults).
//
// Failure path: ErrNoResults, ErrRateLimited, or any other search error
// emits a LoadedSong{Error} via the outbox so queue.onLoadedSong reaps the
// stub. The refiller re-observes the short queue and tries again.
type Service struct {
	client     *Client
	db         *sql.DB
	bus        *bus.Bus
	dispatcher *bus.OutboxDispatcher
	logw       *discovery.Writer
	log        *slog.Logger
}

// NewService wires a Service. Pass nil *discovery.Writer to skip
// observability (tests).
func NewService(c *Client, sqlDB *sql.DB, b *bus.Bus, d *bus.OutboxDispatcher, logw *discovery.Writer) *Service {
	return &Service{
		client:     c,
		db:         sqlDB,
		bus:        b,
		dispatcher: d,
		logw:       logw,
		log:        slog.Default().With("mod", "discogs"),
	}
}

// StartWorkers subscribes to DiscoveryIntent with `workers` goroutines.
// The subscriber name intentionally matches bandcamp's pattern
// (<module>/discovery-intent) for consistency in bus traces.
func (s *Service) StartWorkers(ctx context.Context, workers int) {
	ch := bus.Subscribe[bus.DiscoveryIntent](s.bus, "discogs/discovery-intent")
	bus.RunPool(ctx, s.bus, "discogs/discovery-intent", workers, ch, s.onDiscoveryIntent)
}

// OnDiscoveryIntent is exported for tests.
func (s *Service) OnDiscoveryIntent(ctx context.Context, ev bus.DiscoveryIntent) error {
	return s.onDiscoveryIntent(ctx, ev)
}

func (s *Service) onDiscoveryIntent(ctx context.Context, ev bus.DiscoveryIntent) error {
	// Strategy filter. Discogs currently handles random only.
	if ev.Strategy != bus.StrategyRandom {
		return nil
	}
	// Source filter.
	if !sourceAllowed(ev.PreferredSources, sourceName) {
		return nil
	}

	result, err := s.client.Search(ctx, ev.Genre)
	if err != nil {
		s.recordSeedFailure(ctx, ev, err)
		if err := s.emitLoadedError(ctx, ev.SongID); err != nil {
			s.log.Error("discogs: emit LoadedSong error failed",
				"song_id", ev.SongID, "err", err)
		}
		if errors.Is(err, ErrNoResults) || errors.Is(err, ErrRateLimited) {
			return nil
		}
		return err
	}

	s.logw.Record(ctx, discovery.Record{
		SongID:   ev.SongID,
		UserID:   ev.UserID,
		Source:   discovery.SourceDiscogs,
		Strategy: string(ev.Strategy),
		Stage:    discovery.StageSeed,
		Query:    ev.Genre,
		Outcome:  discovery.OutcomeOK,
		Rung:     -1,
		Reason: func() string {
			if result.CatalogNumber != "" {
				return "picked release with catno " + result.CatalogNumber
			}
			return "picked release without catno"
		}(),
	})

	out := bus.RequestDownload{
		SongID:        ev.SongID,
		Title:         result.Title,
		Artist:        result.Artist,
		CatalogNumber: result.CatalogNumber,
	}
	if err := bus.Publish(ctx, s.bus, out, bus.PublishOpts{
		SendTimeout: 100 * time.Millisecond,
	}); err != nil {
		s.log.Warn("discogs: publish failed", "song_id", ev.SongID, "err", err)
	}
	return nil
}

// recordSeedFailure logs a seed-stage failure to discovery_log. Maps error
// kinds to outcome values so aggregations can distinguish rate-limit blips
// from genuine "Discogs has nothing for this genre" misses.
func (s *Service) recordSeedFailure(ctx context.Context, ev bus.DiscoveryIntent, err error) {
	outcome := discovery.OutcomeError
	switch {
	case errors.Is(err, ErrNoResults):
		outcome = discovery.OutcomeNoResults
	case errors.Is(err, ErrRateLimited):
		outcome = discovery.OutcomeRateLimited
	}
	s.logw.Record(ctx, discovery.Record{
		SongID:   ev.SongID,
		UserID:   ev.UserID,
		Source:   discovery.SourceDiscogs,
		Strategy: string(ev.Strategy),
		Stage:    discovery.StageSeed,
		Query:    ev.Genre,
		Outcome:  outcome,
		Rung:     -1,
		Reason:   err.Error(),
	})
}

// emitLoadedError writes a LoadedSong{Error} row and wakes the dispatcher.
// Mirrors bandcamp's cleanup path.
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

// sourceAllowed returns true if prefs is empty or contains want.
// Empty means "any seeder is fine"; a non-empty list restricts routing.
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
