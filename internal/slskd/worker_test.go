package slskd_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/bus"
	"github.com/macabc/muzika/internal/db"
	"github.com/macabc/muzika/internal/slskd"
	"github.com/macabc/muzika/internal/soulseek"
)

func migrationsURL(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return "file://" + filepath.Join(filepath.Dir(file), "..", "..", "migrations")
}

func setupDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "muzika-test.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := db.Migrate(d, migrationsURL(t)); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return d
}

// fakeClient implements soulseek.Client for tests. Each response is scripted.
type fakeClient struct {
	mu          sync.Mutex
	searchResp  []soulseek.SearchResult
	searchErr   error
	downloadErr error
	states      []soulseek.DownloadState
	stateIdx    int
}

func (f *fakeClient) Search(_ context.Context, _ string, _ time.Duration) ([]soulseek.SearchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.searchResp, nil
}

func (f *fakeClient) Download(_ context.Context, peer, filename string, _ int64) (soulseek.DownloadHandle, error) {
	if f.downloadErr != nil {
		return soulseek.DownloadHandle{}, f.downloadErr
	}
	return soulseek.DownloadHandle{ID: peer + "|" + filename}, nil
}

func (f *fakeClient) DownloadStatus(_ context.Context, _ soulseek.DownloadHandle) (soulseek.DownloadState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.states) == 0 {
		return soulseek.DownloadState{}, errors.New("no state scripted")
	}
	st := f.states[f.stateIdx]
	if f.stateIdx < len(f.states)-1 {
		f.stateIdx++
	}
	return st, nil
}

// TestWorker_HappyPath: search returns one good peer, download completes,
// LoadedSong outbox row is written with status=completed and the filePath.
func TestWorker_HappyPath(t *testing.T) {
	d := setupDB(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)

	songID := uuid.New()
	// Seed a stub song (worker doesn't require one, but this matches the
	// real flow where queue.Refiller has already inserted a stub).
	if _, err := d.Exec(`INSERT INTO queue_songs (id) VALUES (?)`, songID.String()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	fc := &fakeClient{
		searchResp: []soulseek.SearchResult{
			{Peer: "peer1", Filename: "/x/song.mp3", Size: 100, FilesShared: 500, QueueLen: 0},
			{Peer: "peer-tiny", Filename: "/y/song.mp3", Size: 100, FilesShared: 0, QueueLen: 0}, // filtered
		},
		states: []soulseek.DownloadState{
			{State: soulseek.DownloadQueued},
			{State: soulseek.DownloadTransferring, Bytes: 50, Size: 100},
			{State: soulseek.DownloadCompleted, Bytes: 100, Size: 100, FilePath: "song.mp3"},
		},
	}

	svc := slskd.NewService(d, fc, "/music", b, nil)

	err := svc.OnRequestSlskdSong(context.Background(), bus.RequestSlskdSong{
		SongID: songID, Title: "Some Title", Artist: "Some Artist",
	})
	if err != nil {
		t.Fatalf("worker: %v", err)
	}

	// Outbox row should exist with status=completed + filePath.
	var payload []byte
	if err := d.QueryRow(`SELECT payload FROM outbox WHERE event_type = ?`, bus.TypeLoadedSong).Scan(&payload); err != nil {
		t.Fatalf("outbox scan: %v", err)
	}
	var ev bus.LoadedSong
	if err := json.Unmarshal(payload, &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.Status != bus.LoadedStatusCompleted {
		t.Errorf("got status %q, want completed", ev.Status)
	}
	if ev.SongID != songID {
		t.Errorf("songID mismatch: %v vs %v", ev.SongID, songID)
	}
	if ev.FilePath != "song.mp3" {
		t.Errorf("filePath=%q, want song.mp3", ev.FilePath)
	}
}

func TestWorker_NoPeers_EmitsError(t *testing.T) {
	d := setupDB(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)

	fc := &fakeClient{searchResp: nil} // empty results

	svc := slskd.NewService(d, fc, "/music", b, nil)

	err := svc.OnRequestSlskdSong(context.Background(), bus.RequestSlskdSong{
		SongID: uuid.New(), Title: "X", Artist: "Y",
	})
	if err != nil {
		t.Fatalf("worker: %v", err)
	}

	var payload []byte
	_ = d.QueryRow(`SELECT payload FROM outbox WHERE event_type = ?`, bus.TypeLoadedSong).Scan(&payload)
	var ev bus.LoadedSong
	_ = json.Unmarshal(payload, &ev)
	if ev.Status != bus.LoadedStatusError {
		t.Errorf("got status %q, want error", ev.Status)
	}
}

func TestWorker_SearchError_EmitsError(t *testing.T) {
	d := setupDB(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)

	fc := &fakeClient{searchErr: errors.New("soulseek unreachable")}

	svc := slskd.NewService(d, fc, "/music", b, nil)

	err := svc.OnRequestSlskdSong(context.Background(), bus.RequestSlskdSong{
		SongID: uuid.New(),
	})
	if err != nil {
		t.Fatalf("worker: %v", err)
	}

	var payload []byte
	_ = d.QueryRow(`SELECT payload FROM outbox WHERE event_type = ?`, bus.TypeLoadedSong).Scan(&payload)
	var ev bus.LoadedSong
	_ = json.Unmarshal(payload, &ev)
	if ev.Status != bus.LoadedStatusError {
		t.Errorf("got status %q, want error", ev.Status)
	}
}

func TestWorker_DownloadFailed_EmitsError(t *testing.T) {
	d := setupDB(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)

	fc := &fakeClient{
		searchResp: []soulseek.SearchResult{
			{Peer: "peer1", Filename: "song.mp3", Size: 100, FilesShared: 500, QueueLen: 0},
		},
		states: []soulseek.DownloadState{
			{State: soulseek.DownloadFailed},
		},
	}

	svc := slskd.NewService(d, fc, "/music", b, nil)

	err := svc.OnRequestSlskdSong(context.Background(), bus.RequestSlskdSong{
		SongID: uuid.New(),
	})
	if err != nil {
		t.Fatalf("worker: %v", err)
	}

	var payload []byte
	_ = d.QueryRow(`SELECT payload FROM outbox WHERE event_type = ?`, bus.TypeLoadedSong).Scan(&payload)
	var ev bus.LoadedSong
	_ = json.Unmarshal(payload, &ev)
	if ev.Status != bus.LoadedStatusError {
		t.Errorf("got status %q, want error", ev.Status)
	}
}
