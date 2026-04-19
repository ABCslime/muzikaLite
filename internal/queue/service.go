package queue

import (
	"context"
	"database/sql"
	"errors"
	"sync"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/bus"
)

// Service owns per-user queues, the song catalog, and listen stats.
//
// Concurrency: queue mutations serialize per user via userLocks. See CLAUDE.md
// for the convention. Any function that mutates queue_entries for a given
// userID MUST defer s.lockFor(userID)() before touching the DB.
type Service struct {
	db               *sql.DB
	repo             *Repo
	bus              *bus.Bus
	dispatcher       *bus.OutboxDispatcher
	musicStoragePath string
	minQueueSize     int
	refiller         *Refiller

	muUsers   sync.Mutex
	userLocks map[uuid.UUID]*sync.Mutex
}

// NewService wires a Service and its refiller.
func NewService(
	db *sql.DB,
	musicPath string,
	minQueueSize int,
	defaultGenre string,
	b *bus.Bus,
	d *bus.OutboxDispatcher,
) *Service {
	repo := NewRepo(db)
	return &Service{
		db:               db,
		repo:             repo,
		bus:              b,
		dispatcher:       d,
		musicStoragePath: musicPath,
		minQueueSize:     minQueueSize,
		refiller:         NewRefiller(repo, b, minQueueSize, defaultGenre),
		userLocks:        make(map[uuid.UUID]*sync.Mutex),
	}
}

// MusicStoragePath exposes the base directory for audio files.
func (s *Service) MusicStoragePath() string { return s.musicStoragePath }

// lockFor returns an unlock function for userID's mutex. Acquire on any
// queue-mutating operation. See CLAUDE.md.
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

// StartWorkers subscribes to UserCreated, UserDeleted, LoadedSong, RequestSlskdSong.
// TODO(port): Phase 6.
func (s *Service) StartWorkers(ctx context.Context) {
	userCreated := bus.Subscribe[bus.UserCreated](s.bus, "queue/user-created")
	userDeleted := bus.Subscribe[bus.UserDeleted](s.bus, "queue/user-deleted")
	loaded := bus.Subscribe[bus.LoadedSong](s.bus, "queue/loaded-song")
	reqSlskd := bus.Subscribe[bus.RequestSlskdSong](s.bus, "queue/request-slskd-song")

	bus.RunPool(ctx, s.bus, "queue/user-created", 1, userCreated, s.onUserCreated)
	bus.RunPool(ctx, s.bus, "queue/user-deleted", 1, userDeleted, s.onUserDeleted)
	bus.RunPool(ctx, s.bus, "queue/loaded-song", 1, loaded, s.onLoadedSong)
	bus.RunPool(ctx, s.bus, "queue/request-slskd-song", 1, reqSlskd, s.onRequestSlskdSong)
}

func (s *Service) onUserCreated(ctx context.Context, ev bus.UserCreated) error       { return nil }
func (s *Service) onUserDeleted(ctx context.Context, ev bus.UserDeleted) error       { return nil }
func (s *Service) onLoadedSong(ctx context.Context, ev bus.LoadedSong) error         { return nil }
func (s *Service) onRequestSlskdSong(ctx context.Context, ev bus.RequestSlskdSong) error {
	// queue keeps a local copy of the metadata update so its song rows stay in sync.
	return nil
}

// --- HTTP-facing methods ---

// GetQueue returns the user's queued songs and triggers a refill if short.
// TODO(port): Phase 6.
func (s *Service) GetQueue(ctx context.Context, userID uuid.UUID) (QueueResponse, error) {
	return QueueResponse{}, errors.New("queue.Service.GetQueue: not implemented")
}

// AddSong inserts a song at a specific position. TODO(port): Phase 6.
func (s *Service) AddSong(ctx context.Context, userID uuid.UUID, req AddSongRequest) error {
	return errors.New("queue.Service.AddSong: not implemented")
}

// MarkSkipped records a skip and triggers refill. TODO(port): Phase 6.
func (s *Service) MarkSkipped(ctx context.Context, userID uuid.UUID, req SongIDRequest) error {
	return errors.New("queue.Service.MarkSkipped: not implemented")
}

// MarkFinished records a completed play and triggers refill. TODO(port): Phase 6.
func (s *Service) MarkFinished(ctx context.Context, userID uuid.UUID, req SongIDRequest) error {
	return errors.New("queue.Service.MarkFinished: not implemented")
}

// CheckQueue runs a refill pass for the user. TODO(port): Phase 6.
func (s *Service) CheckQueue(ctx context.Context, userID uuid.UUID) error {
	return errors.New("queue.Service.CheckQueue: not implemented")
}

// ResolveSongPath returns the absolute filesystem path for song ID.
// TODO(port): Phase 6.
func (s *Service) ResolveSongPath(ctx context.Context, songID uuid.UUID) (string, error) {
	return "", errors.New("queue.Service.ResolveSongPath: not implemented")
}

// IsLiked returns whether userID has liked songID. TODO(port): Phase 6.
func (s *Service) IsLiked(ctx context.Context, userID, songID uuid.UUID) (bool, error) {
	return false, errors.New("queue.Service.IsLiked: not implemented")
}

// Like sets liked=true and publishes LikedSong (via outbox). TODO(port): Phase 6.
func (s *Service) Like(ctx context.Context, userID, songID uuid.UUID) error {
	return errors.New("queue.Service.Like: not implemented")
}

// Unlike sets liked=false and publishes UnlikedSong (via outbox). TODO(port): Phase 6.
func (s *Service) Unlike(ctx context.Context, userID, songID uuid.UUID) error {
	return errors.New("queue.Service.Unlike: not implemented")
}
