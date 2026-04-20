// Package slskd is a thin wrapper around internal/soulseek.Client. It owns
// the RequestSlskdSong worker pool: search via the Soulseek backend, pick a
// peer, download the file, and emit LoadedSong (via outbox).
//
// The backend implementation (slskd daemon vs. gosk) is selected in main.go
// and passed in here as a soulseek.Client interface.
package slskd

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/bus"
	"github.com/macabc/muzika/internal/db"
	"github.com/macabc/muzika/internal/soulseek"
)

// Tunables. Small constants kept here rather than in config to keep
// Phase-4 surface narrow; can graduate to env if needed.
const (
	searchWindow       = 10 * time.Second
	pollInterval       = 2 * time.Second
	downloadMaxTimeout = 5 * time.Minute
	peerMinFiles       = 1    // avoid peers sharing 0 files
	peerMaxQueue       = 50   // avoid peers with insane queues
)

// Service owns the RequestSlskdSong worker pool.
type Service struct {
	db               *sql.DB
	soulseek         soulseek.Client
	bus              *bus.Bus
	dispatcher       *bus.OutboxDispatcher
	musicStoragePath string
	log              *slog.Logger
}

// NewService wires a Service.
func NewService(
	sqlDB *sql.DB,
	sk soulseek.Client,
	musicStoragePath string,
	b *bus.Bus,
	d *bus.OutboxDispatcher,
) *Service {
	return &Service{
		db:               sqlDB,
		soulseek:         sk,
		bus:              b,
		dispatcher:       d,
		musicStoragePath: musicStoragePath,
		log:              slog.Default().With("mod", "slskd"),
	}
}

// StartWorkers subscribes to RequestSlskdSong with `workers` goroutines.
func (s *Service) StartWorkers(ctx context.Context, workers int) {
	ch := bus.Subscribe[bus.RequestSlskdSong](s.bus, "slskd/request-slskd-song")
	bus.RunPool(ctx, s.bus, "slskd/request-slskd-song", workers, ch, s.onRequestSlskdSong)
}

// OnRequestSlskdSong is exported for tests.
func (s *Service) OnRequestSlskdSong(ctx context.Context, ev bus.RequestSlskdSong) error {
	return s.onRequestSlskdSong(ctx, ev)
}

func (s *Service) onRequestSlskdSong(ctx context.Context, ev bus.RequestSlskdSong) error {
	// Build a sensible query and search.
	query := ev.Artist + " " + ev.Title
	results, err := s.soulseek.Search(ctx, query, searchWindow)
	if err != nil {
		s.log.Error("soulseek search failed", "song_id", ev.SongID, "err", err)
		return s.emitError(ctx, ev.SongID)
	}

	peer, file := pickBest(results)
	if peer == "" {
		s.log.Info("no suitable peer", "song_id", ev.SongID)
		return s.emitError(ctx, ev.SongID)
	}

	h, err := s.soulseek.Download(ctx, peer, file.Filename, file.Size)
	if err != nil {
		s.log.Error("download start failed", "song_id", ev.SongID, "err", err)
		return s.emitError(ctx, ev.SongID)
	}

	filePath, err := s.waitForDownload(ctx, h)
	if err != nil {
		s.log.Error("download wait failed", "song_id", ev.SongID, "err", err)
		return s.emitError(ctx, ev.SongID)
	}

	return s.emitCompleted(ctx, ev.SongID, filePath)
}

// waitForDownload polls DownloadStatus until the transfer reaches a terminal
// state or downloadMaxTimeout elapses.
func (s *Service) waitForDownload(ctx context.Context, h soulseek.DownloadHandle) (string, error) {
	deadline := time.Now().Add(downloadMaxTimeout)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		state, err := s.soulseek.DownloadStatus(ctx, h)
		if err != nil {
			return "", err
		}
		switch state.State {
		case soulseek.DownloadCompleted:
			return state.FilePath, nil
		case soulseek.DownloadFailed:
			return "", errors.New("slskd: download failed")
		}
		if time.Now().After(deadline) {
			return "", errors.New("slskd: download timeout")
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

// pickBest filters results by peer quality heuristics, then returns the peer
// and file with the most shared files (a rough proxy for peer reliability).
func pickBest(results []soulseek.SearchResult) (string, soulseek.SearchResult) {
	var candidates []soulseek.SearchResult
	for _, r := range results {
		if r.FilesShared < peerMinFiles {
			continue
		}
		if r.QueueLen > peerMaxQueue {
			continue
		}
		candidates = append(candidates, r)
	}
	if len(candidates) == 0 {
		return "", soulseek.SearchResult{}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].FilesShared != candidates[j].FilesShared {
			return candidates[i].FilesShared > candidates[j].FilesShared
		}
		return candidates[i].QueueLen < candidates[j].QueueLen
	})
	return candidates[0].Peer, candidates[0]
}

// ---- outbox emissions ----

func (s *Service) emitCompleted(ctx context.Context, songID uuid.UUID, filePath string) error {
	return s.emit(ctx, bus.LoadedSong{
		SongID:   songID,
		FilePath: filePath,
		Status:   bus.LoadedStatusCompleted,
	})
}

func (s *Service) emitError(ctx context.Context, songID uuid.UUID) error {
	return s.emit(ctx, bus.LoadedSong{
		SongID: songID,
		Status: bus.LoadedStatusError,
	})
}

func (s *Service) emit(ctx context.Context, ev bus.LoadedSong) error {
	err := db.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		return bus.InsertOutboxTx(ctx, tx, bus.TypeLoadedSong, ev)
	})
	if err != nil {
		return fmt.Errorf("emit LoadedSong: %w", err)
	}
	if s.dispatcher != nil {
		s.dispatcher.Wake()
	}
	return nil
}
