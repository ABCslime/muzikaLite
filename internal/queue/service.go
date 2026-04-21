package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/bus"
	"github.com/macabc/muzika/internal/db"
)

// ErrNoFile is returned by ResolveSongPath when the song hasn't been downloaded.
var ErrNoFile = errors.New("queue: song has no file yet")

// ErrEmptyQuery is returned by Search when the user-supplied query
// collapses to empty after normalization + long-words fallback.
var ErrEmptyQuery = errors.New("queue: empty query")

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

// NewService wires a Service and its refiller with Bandcamp-only routing.
// Retained for tests. Production calls NewServiceFull from main.go.
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
	return NewServiceWithDiscogs(ctx, sqlDB, musicPath, minQueueSize, defaultGenre, b, d, false, 0)
}

// NewServiceWithDiscogs wires a Service whose refiller may route DiscoveryIntents
// to Discogs with probability discogsWeight. v0.4 PR 2 entry point. Retained
// for tests; production uses NewServiceFull to also inject per-user prefs.
func NewServiceWithDiscogs(
	ctx context.Context,
	sqlDB *sql.DB,
	musicPath string,
	minQueueSize int,
	defaultGenre string,
	b *bus.Bus,
	d *bus.OutboxDispatcher,
	discogsEnabled bool,
	discogsWeight float64,
) *Service {
	return NewServiceFull(ctx, sqlDB, musicPath, minQueueSize,
		defaultGenre, defaultGenre, b, d, discogsEnabled, discogsWeight, nil)
}

// NewServiceFull is the v0.4.1 entry point: separate Bandcamp vs Discogs
// default genres (from MUZIKA_BANDCAMP_DEFAULT_TAGS[0] and
// MUZIKA_DISCOGS_DEFAULT_GENRES[0] respectively), plus a PreferredGenres
// lookup so the refiller biases toward user preferences when set.
// Pass prefs=nil to disable the pref lookup (legacy / tests).
func NewServiceFull(
	ctx context.Context,
	sqlDB *sql.DB,
	musicPath string,
	minQueueSize int,
	defaultBandcamp, defaultDiscogs string,
	b *bus.Bus,
	d *bus.OutboxDispatcher,
	discogsEnabled bool,
	discogsWeight float64,
	prefs PreferredGenres,
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
		refiller: NewRefillerFull(
			repo, b, minQueueSize,
			defaultBandcamp, defaultDiscogs,
			log.With("sub", "refiller"),
			discogsEnabled, discogsWeight, prefs,
		),
		log:       log,
		svcCtx:    ctx,
		userLocks: make(map[uuid.UUID]*sync.Mutex),
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
	// publishes DiscoveryIntent events. The rest of the fan-out (seeder →
	// download worker → LoadedSong → onLoadedSong) completes async.
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
		return s.appendForRequester(ctx, ev.SongID, ev.Relaxed)
	case bus.LoadedStatusError:
		// Delete the stub; cascade removes any queue_entries rows.
		if err := s.repo.DeleteSong(ctx, ev.SongID); err != nil {
			return err
		}
		return nil
	case bus.LoadedStatusNotFound:
		// v0.4.1 PR B. Don't delete the stub — the user needs to see the
		// entry with status='not_found' so they know their search failed
		// rather than silently vanishing. The entry dismissal is a
		// separate DELETE /api/queue/queue/{id} the user triggers.
		userID, ok, err := s.repo.GetSongRequester(ctx, ev.SongID)
		if err != nil {
			return fmt.Errorf("get requester for not_found: %w", err)
		}
		if !ok {
			// No requester means nobody's waiting on this — just drop it.
			return s.repo.DeleteSong(ctx, ev.SongID)
		}
		defer s.lockFor(userID)()
		return s.repo.MarkNotFound(ctx, userID, ev.SongID)
	default:
		return fmt.Errorf("unknown LoadedSong status: %q", ev.Status)
	}
}

// appendForRequester reads the stub's requesting_user_id and surfaces the
// completed song in that user's queue and only that user's. If the stub
// has no requester (legacy rows, or rows inserted outside the refiller
// path), we log and skip — those belong to no queue by definition.
//
// v0.4.1 PR B: if a probing queue_entries row already exists (created by
// onRequestDownload for StrategySearch intents), promote it to ready
// rather than inserting a duplicate. For passive refill there's no
// pre-existing entry, so we fall through to AppendEntry{,Relaxed}.
//
// relaxed (v0.4 PR 3) is the LoadedSong.Relaxed flag: true iff the
// download worker had to fall back to the relaxed gate AND the origin
// was a user-initiated search. For passive refill, the download worker
// passes false regardless — ROADMAP §v0.4 item 6.
func (s *Service) appendForRequester(ctx context.Context, songID uuid.UUID, relaxed bool) error {
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

	// Try to promote a probing entry first (search path). If there isn't
	// one, ErrNotFound is expected (passive refill path).
	if err := s.repo.PromoteToReady(ctx, userID, songID, relaxed); err == nil {
		return nil
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}

	// No probing entry — insert a new ready one.
	var appendErr error
	if relaxed {
		appendErr = s.repo.AppendEntryRelaxed(ctx, userID, songID)
	} else {
		appendErr = s.repo.AppendEntry(ctx, userID, songID)
	}
	if appendErr != nil && !errors.Is(appendErr, ErrDuplicate) {
		return appendErr
	}
	return nil
}

func (s *Service) onRequestDownload(ctx context.Context, ev bus.RequestDownload) error {
	// Fan-out: a seeder published this; the download package consumes it
	// to drive the transfer, we consume it to keep metadata in sync with
	// the search result. UPDATE misses if the stub was already deleted —
	// that's fine.
	if err := s.repo.UpdateSongMetadata(ctx, ev.SongID, ev.Title, ev.Artist); err != nil {
		return err
	}

	// v0.4.1 PR B: user-initiated search gets an early queue entry with
	// status='probing'. Immediate UI feedback — the user sees the artist/
	// title the moment Discogs picks it, rather than waiting ~30s for the
	// download to finish. Passive refill keeps the old flow (entry inserts
	// only on LoadedSong{Completed}).
	if ev.Strategy != bus.StrategySearch {
		return nil
	}
	userID, ok, err := s.repo.GetSongRequester(ctx, ev.SongID)
	if err != nil || !ok {
		// No requester means nobody to surface it to. Nothing to do.
		return nil
	}
	defer s.lockFor(userID)()
	if err := s.repo.InsertProbingEntry(ctx, userID, ev.SongID); err != nil && !errors.Is(err, ErrDuplicate) {
		// Not fatal — the ready-state promotion path in appendForRequester
		// will insert a fresh ready entry if this failed.
		s.log.Warn("queue: insert probing entry failed",
			"song_id", ev.SongID, "user_id", userID, "err", err)
	}
	return nil
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
		status := e.Status
		if status == "" {
			// Legacy rows (pre-migration-0006) have empty string — treat
			// as "ready" so the UI renders them normally.
			status = "ready"
		}
		out.Songs = append(out.Songs, SongDTO{
			ID: sg.ID, Title: sg.Title, Artist: sg.Artist,
			Album: sg.Album, Genre: sg.Genre, Duration: sg.Duration,
			Relaxed: e.Relaxed,
			Status:  status,
		})
	}
	// Fire-and-forget refill — don't block the response. svcCtx outlives the
	// HTTP request but is cancelled on shutdown.
	go s.refiller.Trigger(s.svcCtx, userID)
	return out, nil
}

// Search handles POST /api/queue/search (v0.4 PR 3). Normalizes the user's
// query, inserts a stub song row, and publishes a DiscoveryIntent with
// Strategy=StrategySearch and PreferredSources=["discogs"] so Bandcamp
// ignores it and Discogs handles it. Returns the stub's UUID so clients
// can correlate the eventual queue entry.
//
// If the normalized query is empty, retry with words > 4 chars (ROADMAP
// §v0.4 item 5). If still empty, return 400 — nothing to search on.
func (s *Service) Search(ctx context.Context, userID uuid.UUID, req SearchRequest) (SearchResponse, error) {
	q := normalizeQuery(req.Query)
	if q == "" {
		q = retryLongWords(normalizeQuery(req.Query))
	}
	if q == "" {
		return SearchResponse{}, ErrEmptyQuery
	}

	stubID := uuid.New()
	if err := s.repo.InsertSongStub(ctx, stubID, "", userID); err != nil {
		return SearchResponse{}, fmt.Errorf("insert stub: %w", err)
	}

	ev := bus.DiscoveryIntent{
		SongID:           stubID,
		UserID:           userID,
		Strategy:         bus.StrategySearch,
		Query:            q,
		PreferredSources: []string{"discogs"},
	}
	err := bus.Publish(ctx, s.bus, ev, bus.PublishOpts{
		SendTimeout: 100 * time.Millisecond,
	})
	if err != nil {
		// Keep the stub — the refiller won't help for a search intent, but
		// the caller sees the stub ID so they can retry. Log and return the
		// SearchResponse anyway; a Publish error on a non-full channel is
		// rare and the Discogs worker will likely still drain when it
		// arrives.
		s.log.Warn("search: publish failed", "user_id", userID, "err", err)
	}
	return SearchResponse{SongID: stubID, Query: q}, nil
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

