package download_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/bus"
	"github.com/macabc/muzika/internal/db"
	"github.com/macabc/muzika/internal/download"
	"github.com/macabc/muzika/internal/soulseek"
)

func setupDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "muzika-test.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := db.MigrateEmbedded(d); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return d
}

// fakeClient implements soulseek.Client for tests.
//
// v0.4 PR 2: responses can be scripted per query string (for ladder tests).
// If responsesByQuery is non-nil, Search uses it — missing keys return
// empty results. Otherwise Search falls back to searchResp (the pre-v0.4
// single-response behavior used by the non-ladder tests).
//
// queriesSeen records every query string Search was called with, in order.
type fakeClient struct {
	mu               sync.Mutex
	searchResp       []soulseek.SearchResult
	responsesByQuery map[string][]soulseek.SearchResult
	queriesSeen      []string
	searchErr        error
	downloadErr      error
	states           []soulseek.DownloadState
	stateIdx         int
}

func (f *fakeClient) Search(_ context.Context, query string, _ time.Duration) ([]soulseek.SearchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queriesSeen = append(f.queriesSeen, query)
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	if f.responsesByQuery != nil {
		return f.responsesByQuery[query], nil
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

// goodResult builds a SearchResult that comfortably passes the default gate.
func goodResult(peer, filename string, queue int) soulseek.SearchResult {
	return soulseek.SearchResult{
		Peer: peer, Filename: filename, Size: 5_000_000, Bitrate: 320, QueueLen: queue,
	}
}

func completedStates(filePath string) []soulseek.DownloadState {
	return []soulseek.DownloadState{
		{State: soulseek.DownloadQueued},
		{State: soulseek.DownloadTransferring, Bytes: 50, Size: 100},
		{State: soulseek.DownloadCompleted, Bytes: 100, Size: 100, FilePath: filePath},
	}
}

// TestWorker_HappyPath: search returns one good peer, download completes,
// LoadedSong outbox row is written with status=completed and the filePath.
func TestWorker_HappyPath(t *testing.T) {
	d := setupDB(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)

	songID := uuid.New()
	if _, err := d.Exec(`INSERT INTO queue_songs (id) VALUES (?)`, songID.String()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	fc := &fakeClient{
		searchResp: []soulseek.SearchResult{
			goodResult("peer1", "/x/song.mp3", 0),
			// Queue too long — rejected by the gate's PeerMaxQueue.
			{Peer: "peer-busy", Filename: "/y/song.mp3", Size: 5_000_000, Bitrate: 320, QueueLen: 9999},
		},
		states: completedStates("song.mp3"),
	}

	svc := download.NewService(d, fc, "/music", b, nil)

	err := svc.OnRequestDownload(context.Background(), bus.RequestDownload{
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

	fc := &fakeClient{searchResp: nil}

	svc := download.NewService(d, fc, "/music", b, nil)

	err := svc.OnRequestDownload(context.Background(), bus.RequestDownload{
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

	svc := download.NewService(d, fc, "/music", b, nil)

	err := svc.OnRequestDownload(context.Background(), bus.RequestDownload{
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

func TestWorker_DownloadFailed_EmitsError(t *testing.T) {
	d := setupDB(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)

	fc := &fakeClient{
		searchResp: []soulseek.SearchResult{goodResult("peer1", "song.mp3", 0)},
		states: []soulseek.DownloadState{
			{State: soulseek.DownloadFailed},
		},
	}

	svc := download.NewService(d, fc, "/music", b, nil)

	err := svc.OnRequestDownload(context.Background(), bus.RequestDownload{
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

// ---- Gate rejection ----

// TestGate_AllBelowFloorStrict_RelaxFallback: every result is 96 kbps (below
// the 192 strict floor) but passes the relaxed 96 floor. Expect the worker
// to succeed via relax-mode fallback.
func TestGate_AllBelowFloorStrict_RelaxFallback(t *testing.T) {
	d := setupDB(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)

	fc := &fakeClient{
		searchResp: []soulseek.SearchResult{
			{Peer: "low", Filename: "a.mp3", Size: 5_000_000, Bitrate: 96, QueueLen: 0},
		},
		states: completedStates("a.mp3"),
	}

	svc := download.NewService(d, fc, "/music", b, nil)

	err := svc.OnRequestDownload(context.Background(), bus.RequestDownload{
		SongID: uuid.New(), Title: "X", Artist: "Y",
	})
	if err != nil {
		t.Fatalf("worker: %v", err)
	}

	var payload []byte
	_ = d.QueryRow(`SELECT payload FROM outbox WHERE event_type = ?`, bus.TypeLoadedSong).Scan(&payload)
	var ev bus.LoadedSong
	_ = json.Unmarshal(payload, &ev)
	if ev.Status != bus.LoadedStatusCompleted {
		t.Errorf("relax-mode should have accepted; got status=%q", ev.Status)
	}
}

// TestGate_AllBelowRelaxFloor_EmitsError: results are 32 kbps, below even
// the relaxed 96 kbps floor. Expect the worker to emit an error.
func TestGate_AllBelowRelaxFloor_EmitsError(t *testing.T) {
	d := setupDB(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)

	fc := &fakeClient{
		searchResp: []soulseek.SearchResult{
			{Peer: "awful", Filename: "a.mp3", Size: 5_000_000, Bitrate: 32, QueueLen: 0},
		},
	}

	svc := download.NewService(d, fc, "/music", b, nil)

	err := svc.OnRequestDownload(context.Background(), bus.RequestDownload{
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
		t.Errorf("got status %q, want error (all below relax floor)", ev.Status)
	}
}

// ---- Ladder ----

// TestLadder_CatnoRungWins: rung 0 (catno) returns rich results, so the
// ladder exits without querying rung 1 or 2.
func TestLadder_CatnoRungWins(t *testing.T) {
	d := setupDB(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)

	fc := &fakeClient{
		responsesByQuery: map[string][]soulseek.SearchResult{
			"WARP-55": {
				goodResult("peer-a", "a.flac", 0),
				goodResult("peer-b", "b.flac", 1),
				goodResult("peer-c", "c.flac", 2),
				goodResult("peer-d", "d.flac", 3),
			},
		},
		states: completedStates("a.flac"),
	}

	svc := download.NewService(d, fc, "/music", b, nil)

	err := svc.OnRequestDownload(context.Background(), bus.RequestDownload{
		SongID: uuid.New(), Title: "Music Has The Right", Artist: "Boards Of Canada",
		CatalogNumber: "WARP-55",
	})
	if err != nil {
		t.Fatalf("worker: %v", err)
	}

	if len(fc.queriesSeen) != 1 || fc.queriesSeen[0] != "WARP-55" {
		t.Errorf("ladder should have exited at rung 0; queries seen: %v", fc.queriesSeen)
	}
}

// TestLadder_FallsThroughToArtistTitle: rung 0 (catno) returns zero passes,
// rung 1 (artist+title) returns enough. The ladder should run rung 0 then
// rung 1 and stop before rung 2.
func TestLadder_FallsThroughToArtistTitle(t *testing.T) {
	d := setupDB(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)

	fc := &fakeClient{
		responsesByQuery: map[string][]soulseek.SearchResult{
			"MADEUP-42": nil, // catno miss
			"Boards Of Canada Music Has The Right": {
				goodResult("peer-a", "a.flac", 0),
				goodResult("peer-b", "b.flac", 1),
				goodResult("peer-c", "c.flac", 2),
				goodResult("peer-d", "d.flac", 3),
			},
		},
		states: completedStates("a.flac"),
	}

	svc := download.NewService(d, fc, "/music", b, nil)

	err := svc.OnRequestDownload(context.Background(), bus.RequestDownload{
		SongID: uuid.New(), Title: "Music Has The Right", Artist: "Boards Of Canada",
		CatalogNumber: "MADEUP-42",
	})
	if err != nil {
		t.Fatalf("worker: %v", err)
	}

	if len(fc.queriesSeen) != 2 {
		t.Fatalf("ladder should stop at rung 1; queries seen: %v", fc.queriesSeen)
	}
	if fc.queriesSeen[0] != "MADEUP-42" {
		t.Errorf("rung 0 query wrong: %q", fc.queriesSeen[0])
	}
	if fc.queriesSeen[1] != "Boards Of Canada Music Has The Right" {
		t.Errorf("rung 1 query wrong: %q", fc.queriesSeen[1])
	}
}

// TestLadder_NoCatnoStartsAtArtistTitle: empty CatalogNumber collapses rung 0;
// rung 1 runs first.
func TestLadder_NoCatnoStartsAtArtistTitle(t *testing.T) {
	d := setupDB(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)

	fc := &fakeClient{
		responsesByQuery: map[string][]soulseek.SearchResult{
			"Artist Title": {
				goodResult("peer-a", "a.flac", 0),
				goodResult("peer-b", "b.flac", 1),
				goodResult("peer-c", "c.flac", 2),
			},
		},
		states: completedStates("a.flac"),
	}

	svc := download.NewService(d, fc, "/music", b, nil)

	err := svc.OnRequestDownload(context.Background(), bus.RequestDownload{
		SongID: uuid.New(), Title: "Title", Artist: "Artist",
	})
	if err != nil {
		t.Fatalf("worker: %v", err)
	}

	if len(fc.queriesSeen) < 1 || fc.queriesSeen[0] != "Artist Title" {
		t.Errorf("first rung should be artist+title; got %v", fc.queriesSeen)
	}
}

// TestLadder_TitleOnlyIsFinalRung: rungs 0 and 1 empty; rung 2 wins.
func TestLadder_TitleOnlyIsFinalRung(t *testing.T) {
	d := setupDB(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)

	fc := &fakeClient{
		responsesByQuery: map[string][]soulseek.SearchResult{
			// catno
			"CAT-1": nil,
			// artist+title
			"Artist Title": nil,
			// title-only
			"Title": {
				goodResult("peer-a", "a.flac", 0),
				goodResult("peer-b", "b.flac", 1),
				goodResult("peer-c", "c.flac", 2),
			},
		},
		states: completedStates("a.flac"),
	}

	svc := download.NewService(d, fc, "/music", b, nil)

	err := svc.OnRequestDownload(context.Background(), bus.RequestDownload{
		SongID: uuid.New(), Title: "Title", Artist: "Artist",
		CatalogNumber: "CAT-1",
	})
	if err != nil {
		t.Fatalf("worker: %v", err)
	}

	if len(fc.queriesSeen) != 3 {
		t.Fatalf("ladder should run all 3 rungs; queries seen: %v", fc.queriesSeen)
	}
	want := []string{"CAT-1", "Artist Title", "Title"}
	for i := range want {
		if fc.queriesSeen[i] != want[i] {
			t.Errorf("rung %d query: got %q, want %q", i, fc.queriesSeen[i], want[i])
		}
	}
}

// TestDiscoveryLog_NilWriterIsSafe: passing nil *discovery.Writer must be a
// no-op — the worker must not panic and no rows must land in discovery_log.
// Production tests pass a real writer; this is a smoke check for the
// tolerated-nil contract.
func TestDiscoveryLog_NilWriterIsSafe(t *testing.T) {
	d := setupDB(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)

	songID := uuid.New()
	if _, err := d.Exec(`INSERT INTO queue_songs (id) VALUES (?)`, songID.String()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	fc := &fakeClient{
		responsesByQuery: map[string][]soulseek.SearchResult{
			"CAT-1":        nil,
			"Artist Title": nil,
			"Title":        {goodResult("peer-a", "a.flac", 0)},
		},
		states: completedStates("a.flac"),
	}

	cfg := download.DefaultConfig()
	cfg.LadderEnough = 1
	svc := download.NewServiceWithConfig(d, fc, "/music", b, nil, nil, cfg)

	if err := svc.OnRequestDownload(context.Background(), bus.RequestDownload{
		SongID: songID, Title: "Title", Artist: "Artist",
		CatalogNumber: "CAT-1",
	}); err != nil {
		t.Fatalf("worker: %v", err)
	}

	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM discovery_log`).Scan(&n)
	if n != 0 {
		t.Errorf("expected 0 discovery_log rows with nil writer, got %d", n)
	}
}

// TestDiscoveryLog_RecordsLadderAndPicked: with a real *discovery.Writer,
// a 3-rung fallthrough should emit one ladder + one gate row per rung, plus
// one picked row at the end.
func TestDiscoveryLog_RecordsLadderAndPicked(t *testing.T) {
	d := setupDB(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)

	songID := uuid.New()
	if _, err := d.Exec(`INSERT INTO queue_songs (id) VALUES (?)`, songID.String()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	fc := &fakeClient{
		responsesByQuery: map[string][]soulseek.SearchResult{
			"CAT-1":        nil,
			"Artist Title": nil,
			"Title":        {goodResult("peer-a", "a.flac", 0)},
		},
		states: completedStates("a.flac"),
	}

	cfg := download.DefaultConfig()
	cfg.LadderEnough = 1
	w := newDiscoveryWriter(d)
	svc := download.NewServiceWithConfig(d, fc, "/music", b, nil, w, cfg)

	if err := svc.OnRequestDownload(context.Background(), bus.RequestDownload{
		SongID: songID, Title: "Title", Artist: "Artist",
		CatalogNumber: "CAT-1",
	}); err != nil {
		t.Fatalf("worker: %v", err)
	}

	// Expect: 3 ladder rows + 3 gate rows + 1 picked row = 7.
	var ladderN, gateN, pickedN int
	_ = d.QueryRow(`SELECT COUNT(*) FROM discovery_log WHERE stage = 'ladder'`).Scan(&ladderN)
	_ = d.QueryRow(`SELECT COUNT(*) FROM discovery_log WHERE stage = 'gate'`).Scan(&gateN)
	_ = d.QueryRow(`SELECT COUNT(*) FROM discovery_log WHERE stage = 'picked'`).Scan(&pickedN)

	if ladderN != 3 {
		t.Errorf("ladder rows: got %d, want 3", ladderN)
	}
	if gateN != 3 {
		t.Errorf("gate rows: got %d, want 3", gateN)
	}
	if pickedN != 1 {
		t.Errorf("picked rows: got %d, want 1", pickedN)
	}

	// Sanity-check a known rung's query text.
	var q string
	_ = d.QueryRow(`SELECT query FROM discovery_log WHERE stage = 'ladder' AND rung = 0`).Scan(&q)
	if q != "CAT-1" {
		t.Errorf("rung 0 query in log = %q, want CAT-1", q)
	}
}
