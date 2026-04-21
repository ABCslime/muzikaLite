// Package download is a thin wrapper around internal/soulseek.Client. It owns
// the RequestDownload worker pool: search via the Soulseek backend, pick a
// peer, download the file, and emit LoadedSong (via outbox).
//
// The name reflects what the package does — turn "title + artist" into a
// downloaded file — not which backend it speaks to. Swapping gosk for a
// different Soulseek client later wouldn't change this package's API.
package download

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
	peerMinFiles       = 1  // avoid peers sharing 0 files
	peerMaxQueue       = 50 // avoid peers with insane queues
)

// Service owns the RequestDownload worker pool.
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
		log:              slog.Default().With("mod", "download"),
	}
}

// StartWorkers subscribes to RequestDownload with `workers` goroutines.
func (s *Service) StartWorkers(ctx context.Context, workers int) {
	ch := bus.Subscribe[bus.RequestDownload](s.bus, "download/request-download")
	bus.RunPool(ctx, s.bus, "download/request-download", workers, ch, s.onRequestDownload)
}

// OnRequestDownload is exported for tests.
func (s *Service) OnRequestDownload(ctx context.Context, ev bus.RequestDownload) error {
	return s.onRequestDownload(ctx, ev)
}

func (s *Service) onRequestDownload(ctx context.Context, ev bus.RequestDownload) error {
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
			return "", errors.New("download: transfer failed")
		}
		if time.Now().After(deadline) {
			return "", errors.New("download: transfer timeout")
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
//
// FilesShared == 0 is treated as "unknown" rather than "zero-share peer":
// the gosk backend leaves it at 0 because the Soulseek wire-level
// FileSearchResponse doesn't carry a per-peer shared count. Rejecting 0s
// here would silently drop every result.
func pickBest(results []soulseek.SearchResult) (string, soulseek.SearchResult) {
	var candidates []soulseek.SearchResult
	for _, r := range results {
		if r.FilesShared > 0 && r.FilesShared < peerMinFiles {
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
