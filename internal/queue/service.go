package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/bus"
	"github.com/macabc/muzika/internal/db"
)

// ErrNoFile is returned by ResolveSongPath when the song hasn't been downloaded.
var ErrNoFile = errors.New("queue: song has no file yet")

// Service owns per-user queues, the song catalog, and listen stats.
//
// Concurrency: queue mutations serialize per user via userLocks. See CLAUDE.md.
//
// svcCtx is the long-lived context the HTTP-facing methods use when they fire
// off a refill goroutine. Using context.Background() there would leak past
// shutdown; reusing the per-request ctx would cancel the refill as soon as
// the HTTP response is written. svcCtx is cancelled by main.go's SIGTERM
// handler, so refills ride the process lifecycle.
type Service struct {
	db               *sql.DB
	repo             *Repo
	bus              *bus.Bus
	dispatcher       *bus.OutboxDispatcher
	musicStoragePath string
	minQueueSize     int
	refiller         *Refiller
	log              *slog.Logger
	svcCtx           context.Context

	muUsers   sync.Mutex
	userLocks map[uuid.UUID]*sync.Mutex
}

// NewService wires a Service and its refiller.
//
// ctx is the process-lifecycle context (main.go's signal.NotifyContext result).
// It's stored and used by the HTTP-facing refill fire-and-forget goroutines
// so they inherit shutdown, unlike context.Background().
func NewService(
	ctx context.Context,
	sqlDB *sql.DB,
	musicPath string,
	minQueueSize int,
	defaultGenre string,
	b *bus.Bus,
	d *bus.OutboxDispatcher,
) *Service {
	log := slog.Default().With("mod", "queue")
	repo := NewRepo(sqlDB)
	return &Service{
		db:               sqlDB,
		repo:             repo,
		bus:              b,
		dispatcher:       d,
		musicStoragePath: musicPath,
		minQueueSize:     minQueueSize,
		refiller:         NewRefiller(repo, b, minQueueSize, defaultGenre, log.With("sub", "refiller")),
		log:              log,
		svcCtx:           ctx,
		userLocks:        make(map[uuid.UUID]*sync.Mutex),
	}
}

// MusicStoragePath exposes the base directory for audio files.
func (s *Service) MusicStoragePath() string { return s.musicStoragePath }

// Repo exposes the repo for tests.
func (s *Service) Repo() *Repo { return s.repo }

// Refiller exposes the refiller for tests.
func (s *Service) Refiller() *Refiller { return s.refiller }

// RefillerBus exposes the service's bus for tests that want to subscribe.
func (s *Service) RefillerBus() *bus.Bus { return s.bus }

// lockFor returns an unlock function for userID's mutex. Acquire on any
// queue-mutating operation per the CLAUDE.md convention.
func (s *Service) lockFor(userID uuid.UUID) func() {
	s.muUsers.Lock()
	m, ok := s.userLocks[userID]
	if !ok {
		m = &sync.Mutex{}
		s.userLocks[userID] = m
	}
	s.muUsers.Unlock()
	m.Lock()
	return m.Unlock
}

// StartWorkers subscribes to UserCreated, UserDeleted, LoadedSong, RequestDownload.
func (s *Service) StartWorkers(ctx context.Context) {
	userCreated := bus.Subscribe[bus.UserCreated](s.bus, "queue/user-created")
	userDeleted := bus.Subscribe[bus.UserDeleted](s.bus, "queue/user-deleted")
	loaded := bus.Subscribe[bus.LoadedSong](s.bus, "queue/loaded-song")
	reqDownload := bus.Subscribe[bus.RequestDownload](s.bus, "queue/request-download")

	bus.RunPool(ctx, s.bus, "queue/user-created", 1, userCreated, s.onUserCreated)
	bus.RunPool(ctx, s.bus, "queue/user-deleted", 1, userDeleted, s.onUserDeleted)
	bus.RunPool(ctx, s.bus, "queue/loaded-song", 1, loaded, s.onLoadedSong)
	bus.RunPool(ctx, s.bus, "queue/request-download", 1, reqDownload, s.onRequestDownload)
}

// Exposed handler aliases for tests.
func (s *Service) OnUserCreated(ctx context.Context, ev bus.UserCreated) error {
	return s.onUserCreated(ctx, ev)
}
func (s *Service) OnUserDeleted(ctx context.Context, ev bus.UserDeleted) error {
	return s.onUserDeleted(ctx, ev)
}
func (s *Service) OnLoadedSong(ctx context.Context, ev bus.LoadedSong) error {
	return s.onLoadedSong(ctx, ev)
}
func (s *Service) OnRequestDownload(ctx context.Context, ev bus.RequestDownload) error {
	return s.onRequestDownload(ctx, ev)
}

// --- event handlers ---

func (s *Service) onUserCreated(ctx context.Context, ev bus.UserCreated) error {
	// Seed the user's queue by triggering the refiller: it inserts stubs and
	// publishes RequestRandomSong events. The rest of the fan-out (Bandcamp
	// search → download worker → LoadedSong → onLoadedSong) completes async.
	s.refiller.Trigger(ctx, ev.UserID)
	return nil
}

func (s *Service) onUserDeleted(_ context.Context, ev bus.UserDeleted) error {
	// FK cascade removed queue_entries and queue_user_songs. Drop the
	// in-memory mutex entry to avoid a slow leak over long uptimes.
	s.muUsers.Lock()
	delete(s.userLocks, ev.UserID)
	s.muUsers.Unlock()
	return nil
}

func (s *Service) onLoadedSong(ctx context.Context, ev bus.LoadedSong) error {
	switch ev.Status {
	case bus.LoadedStatusCompleted:
		if err := s.repo.UpdateSongFile(ctx, ev.SongID, ev.FilePath); err != nil {
			return err
		}
		return s.appendForRequester(ctx, ev.SongID)
	case bus.LoadedStatusError:
		// Delete the stub; cascade removes any queue_entries rows.
		if err := s.repo.DeleteSong(ctx, ev.SongID); err != nil {
			return err
		}
		return nil
	default:
		return fmt.Errorf("unknown LoadedSong status: %q", ev.Status)
	}
}

// appendForRequester reads the stub's requesting_user_id and appends the
// completed song to that user's queue and only that user's. If the stub has
// no requester (legacy rows, or rows inserted outside the refiller path), we
// log and skip — those belong to no queue by definition.
func (s *Service) appendForRequester(ctx context.Context, songID uuid.UUID) error {
	userID, ok, err := s.repo.GetSongRequester(ctx, songID)
	if err != nil {
		return fmt.Errorf("get requester: %w", err)
	}
	if !ok {
		// No owner recorded. Don't guess; the refiller on the next Trigger
		// pass will regenerate whatever was missing.
		s.log.Debug("loaded song has no requester; not auto-attaching",
			"song_id", songID)
		return nil
	}
	defer s.lockFor(userID)()
	if err := s.repo.AppendEntry(ctx, userID, songID); err != nil && !errors.Is(err, ErrDuplicate) {
		return err
	}
	return nil
}

func (s *Service) onRequestDownload(ctx context.Context, ev bus.RequestDownload) error {
	// Fan-out: bandcamp published this; the download package consumes it to
	// drive the transfer, we consume it to keep metadata in sync with the
	// search result. UPDATE misses if the stub was already deleted — that's fine.
	return s.repo.UpdateSongMetadata(ctx, ev.SongID, ev.Title, ev.Artist)
}

// --- HTTP-facing methods ---

// GetQueue returns the user's queued songs and triggers a refill if short.
func (s *Service) GetQueue(ctx context.Context, userID uuid.UUID) (QueueResponse, error) {
	entries, err := s.repo.ListEntries(ctx, userID)
	if err != nil {
		return QueueResponse{}, err
	}
	out := QueueResponse{Songs: make([]SongDTO, 0, len(entries))}
	for _, e := range entries {
		sg, err := s.repo.GetSong(ctx, e.SongID)
		if err != nil {
			continue // skip missing songs rather than failing the whole list
		}
		out.Songs = append(out.Songs, SongDTO{
			ID: sg.ID, Title: sg.Title, Artist: sg.Artist,
			Album: sg.Album, Genre: sg.Genre, Duration: sg.Duration,
		})
	}
	// Fire-and-forget refill — don't block the response. svcCtx outlives the
	// HTTP request but is cancelled on shutdown.
	go s.refiller.Trigger(s.svcCtx, userID)
	return out, nil
}

// AddSong inserts a song at a specific position (appended to the end).
func (s *Service) AddSong(ctx context.Context, userID uuid.UUID, req AddSongRequest) error {
	defer s.lockFor(userID)()
	err := s.repo.AppendEntry(ctx, userID, req.SongID)
	if err != nil && !errors.Is(err, ErrDuplicate) {
		return err
	}
	return nil
}

// MarkSkipped records a skip and removes the song from the queue atomically,
// then refills.
//
// The two mutations (skipped-flag upsert + queue-entry delete) are wrapped in
// one transaction and serialized under the user's mutex so a concurrent
// MarkFinished can't interleave and leave listen_count + skipped both bumped.
func (s *Service) MarkSkipped(ctx context.Context, userID uuid.UUID, req SongIDRequest) error {
	defer s.lockFor(userID)()
	err := db.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		if err := s.repo.MarkSkippedTx(ctx, tx, userID, req.SongID); err != nil {
			return err
		}
		if err := s.repo.RemoveEntryTx(ctx, tx, userID, req.SongID); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	go s.refiller.Trigger(s.svcCtx, userID)
	return nil
}

// MarkFinished records a completed play and removes the song atomically,
// then refills.
//
// Same tx + mutex contract as MarkSkipped.
func (s *Service) MarkFinished(ctx context.Context, userID uuid.UUID, req SongIDRequest) error {
	defer s.lockFor(userID)()
	err := db.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		if err := s.repo.IncrementListenCountTx(ctx, tx, userID, req.SongID); err != nil {
			return err
		}
		if err := s.repo.RemoveEntryTx(ctx, tx, userID, req.SongID); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	go s.refiller.Trigger(s.svcCtx, userID)
	return nil
}

// CheckQueue runs a refill pass for the user (manual trigger).
func (s *Service) CheckQueue(ctx context.Context, userID uuid.UUID) error {
	s.refiller.Trigger(ctx, userID)
	return nil
}

// RemoveSong removes songID from userID's queue without recording it as
// played or skipped — used for explicit user-driven removals from the UI.
// Triggers a refill pass so the queue doesn't stay short.
func (s *Service) RemoveSong(ctx context.Context, userID, songID uuid.UUID) error {
	defer s.lockFor(userID)()
	if err := s.repo.RemoveEntry(ctx, userID, songID); err != nil {
		return err
	}
	go s.refiller.Trigger(s.svcCtx, userID)
	return nil
}

// ResolveSongPath returns the absolute filesystem path for songID.
func (s *Service) ResolveSongPath(ctx context.Context, songID uuid.UUID) (string, error) {
	sg, err := s.repo.GetSong(ctx, songID)
	if err != nil {
		return "", err
	}
	if sg.URL == "" {
		return "", ErrNoFile
	}
	if filepath.IsAbs(sg.URL) {
		return sg.URL, nil
	}
	return filepath.Join(s.musicStoragePath, sg.URL), nil
}

// IsLiked returns whether userID has liked songID.
func (s *Service) IsLiked(ctx context.Context, userID, songID uuid.UUID) (bool, error) {
	return s.repo.GetLiked(ctx, userID, songID)
}

// Like sets liked=true and publishes LikedSong via the outbox.
func (s *Service) Like(ctx context.Context, userID, songID uuid.UUID) error {
	return s.setLiked(ctx, userID, songID, true)
}

// Unlike sets liked=false and publishes UnlikedSong via the outbox.
func (s *Service) Unlike(ctx context.Context, userID, songID uuid.UUID) error {
	return s.setLiked(ctx, userID, songID, false)
}

func (s *Service) setLiked(ctx context.Context, userID, songID uuid.UUID, liked bool) error {
	// The SQL (upsert + outbox insert in one tx) is already atomic. The lock
	// is here for consistency with the documented "queue mutations serialize
	// per user" convention — future changes shouldn't have to re-discover
	// the invariant by reading the schema.
	defer s.lockFor(userID)()
	err := db.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		val := 0
		if liked {
			val = 1
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO queue_user_songs (user_id, song_id, liked)
			 VALUES (?, ?, ?)
			 ON CONFLICT(user_id, song_id) DO UPDATE SET liked = excluded.liked`,
			userID.String(), songID.String(), val); err != nil {
			return fmt.Errorf("upsert liked: %w", err)
		}
		if liked {
			return bus.InsertOutboxTx(ctx, tx, bus.TypeLikedSong, bus.LikedSong{
				UserID: userID, SongID: songID,
			})
		}
		return bus.InsertOutboxTx(ctx, tx, bus.TypeUnlikedSong, bus.UnlikedSong{
			UserID: userID, SongID: songID,
		})
	})
	if err != nil {
		return err
	}
	if s.dispatcher != nil {
		s.dispatcher.Wake()
	}
	return nil
}

