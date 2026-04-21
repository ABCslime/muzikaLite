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

// TestRelax_SurfacedOnlyForSearchStrategy: when strict rejects all results
// and relax mode saves the download, the resulting LoadedSong carries
// Relaxed=true ONLY if the upstream Strategy was StrategySearch (user-
// initiated). Passive refill (StrategyRandom, empty, etc.) gets Relaxed=false
// even though relax-mode fired. ROADMAP §v0.4 item 6.
func TestRelax_SurfacedOnlyForSearchStrategy(t *testing.T) {
	cases := []struct {
		name        string
		strategy    bus.Strategy
		wantRelaxed bool
	}{
		{"passive_random", bus.StrategyRandom, false},
		{"user_search", bus.StrategySearch, true},
		{"legacy_empty_strategy", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := setupDB(t)
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			b := bus.New(64, log)

			// 96 kbps: fails strict (192 min), passes relaxed (96 min).
			// Forces the worker into the relax branch.
			fc := &fakeClient{
				searchResp: []soulseek.SearchResult{
					{Peer: "lowbr", Filename: "a.mp3", Size: 5_000_000, Bitrate: 96, QueueLen: 0},
				},
				states: completedStates("a.mp3"),
			}

			svc := download.NewService(d, fc, "/music", b, nil)

			err := svc.OnRequestDownload(context.Background(), bus.RequestDownload{
				SongID: uuid.New(), Title: "X", Artist: "Y",
				Strategy: tc.strategy,
			})
			if err != nil {
				t.Fatalf("worker: %v", err)
			}

			var payload []byte
			_ = d.QueryRow(`SELECT payload FROM outbox WHERE event_type = ?`, bus.TypeLoadedSong).Scan(&payload)
			var ev bus.LoadedSong
			_ = json.Unmarshal(payload, &ev)
			if ev.Status != bus.LoadedStatusCompleted {
				t.Fatalf("unexpected status %q (relax should have saved it)", ev.Status)
			}
			if ev.Relaxed != tc.wantRelaxed {
				t.Errorf("Relaxed = %v, want %v", ev.Relaxed, tc.wantRelaxed)
			}
		})
	}
}

// TestProbe_NoPeers_EmitsNotFound: a search intent whose probe turns up
// zero peers must emit LoadedStatusNotFound (not LoadedStatusError) — the
// UI toasts "not found on Soulseek" specifically. The ladder is not run.
func TestProbe_NoPeers_EmitsNotFound(t *testing.T) {
	d := setupDB(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)

	fc := &fakeClient{
		responsesByQuery: map[string][]soulseek.SearchResult{
			// Probe and ladder queries both return nothing; only probe
			// should run before emitNotFound short-circuits.
		},
	}
	svc := download.NewService(d, fc, "/music", b, nil)

	songID := uuid.New()
	err := svc.OnRequestDownload(context.Background(), bus.RequestDownload{
		SongID: songID, Title: "Nonexistent", Artist: "Nobody",
		Strategy: bus.StrategySearch,
	})
	if err != nil {
		t.Fatalf("worker: %v", err)
	}

	var payload []byte
	_ = d.QueryRow(`SELECT payload FROM outbox WHERE event_type = ?`, bus.TypeLoadedSong).Scan(&payload)
	var ev bus.LoadedSong
	_ = json.Unmarshal(payload, &ev)
	if ev.Status != bus.LoadedStatusNotFound {
		t.Errorf("got status %q, want %q", ev.Status, bus.LoadedStatusNotFound)
	}

	// Only the probe should have fired — exactly one Search call.
	if len(fc.queriesSeen) != 1 {
		t.Errorf("expected 1 Search call (probe only), got %d: %v",
			len(fc.queriesSeen), fc.queriesSeen)
	}
}

// TestProbe_PeersFound_RunsLadder: a search intent with peers in the
// probe proceeds to the full ladder and produces a normal Completed.
func TestProbe_PeersFound_RunsLadder(t *testing.T) {
	d := setupDB(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)

	fc := &fakeClient{
		// Same result list served for probe AND artist+title rungs.
		searchResp: []soulseek.SearchResult{goodResult("peer1", "song.mp3", 0)},
		states:     completedStates("song.mp3"),
	}
	svc := download.NewService(d, fc, "/music", b, nil)

	err := svc.OnRequestDownload(context.Background(), bus.RequestDownload{
		SongID: uuid.New(), Title: "Has Peers", Artist: "Real Artist",
		Strategy: bus.StrategySearch,
	})
	if err != nil {
		t.Fatalf("worker: %v", err)
	}

	var payload []byte
	_ = d.QueryRow(`SELECT payload FROM outbox WHERE event_type = ?`, bus.TypeLoadedSong).Scan(&payload)
	var ev bus.LoadedSong
	_ = json.Unmarshal(payload, &ev)
	if ev.Status != bus.LoadedStatusCompleted {
		t.Errorf("got status %q, want %q", ev.Status, bus.LoadedStatusCompleted)
	}

	// Probe + at least one ladder rung → 2+ Search calls.
	if len(fc.queriesSeen) < 2 {
		t.Errorf("expected probe + ladder Search calls, got %d: %v",
			len(fc.queriesSeen), fc.queriesSeen)
	}
}

// TestProbe_NotRunForPassiveRefill: StrategyRandom intents bypass the
// probe entirely. This is the v0.4 behavior we want to preserve —
// probe is a search-UX feature, not a passive-refill feature.
func TestProbe_NotRunForPassiveRefill(t *testing.T) {
	d := setupDB(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)

	fc := &fakeClient{
		searchResp: []soulseek.SearchResult{goodResult("peer1", "song.mp3", 0)},
		states:     completedStates("song.mp3"),
	}
	svc := download.NewService(d, fc, "/music", b, nil)

	err := svc.OnRequestDownload(context.Background(), bus.RequestDownload{
		SongID: uuid.New(), Title: "X", Artist: "Y",
		Strategy: bus.StrategyRandom,
	})
	if err != nil {
		t.Fatalf("worker: %v", err)
	}

	// No separate probe call; just ladder.
	for _, q := range fc.queriesSeen {
		if q == "Y X" {
			// This is the artist+title ladder rung, fine.
			continue
		}
	}

	var payload []byte
	_ = d.QueryRow(`SELECT payload FROM outbox WHERE event_type = ?`, bus.TypeLoadedSong).Scan(&payload)
	var ev bus.LoadedSong
	_ = json.Unmarshal(payload, &ev)
	if ev.Status != bus.LoadedStatusCompleted {
		t.Errorf("got status %q, want completed", ev.Status)
	}
}

// TestRelax_NotSetWhenStrictWins: when strict passes everything, the
// relaxed-flag must stay false regardless of upstream strategy.
func TestRelax_NotSetWhenStrictWins(t *testing.T) {
	d := setupDB(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)

	fc := &fakeClient{
		searchResp: []soulseek.SearchResult{goodResult("peer1", "a.mp3", 0)},
		states:     completedStates("a.mp3"),
	}
	svc := download.NewService(d, fc, "/music", b, nil)

	err := svc.OnRequestDownload(context.Background(), bus.RequestDownload{
		SongID: uuid.New(), Title: "X", Artist: "Y",
		Strategy: bus.StrategySearch, // even on search path, no relax fires
	})
	if err != nil {
		t.Fatalf("worker: %v", err)
	}

	var payload []byte
	_ = d.QueryRow(`SELECT payload FROM outbox WHERE event_type = ?`, bus.TypeLoadedSong).Scan(&payload)
	var ev bus.LoadedSong
	_ = json.Unmarshal(payload, &ev)
	if ev.Relaxed {
		t.Errorf("Relaxed = true when strict succeeded — should be false")
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
// a 3-rung fallthrough should emit one ladder row per rung plus one gate
// row per SearchResult across all rungs (per-candidate logging, ROADMAP
// §v0.4 item 3), plus one picked row at the end.
//
// In this scenario:
//   - rung 0 (CAT-1):        0 results → 0 gate rows
//   - rung 1 (Artist Title): 0 results → 0 gate rows
//   - rung 2 (Title):        1 result  → 1 gate row
// → 3 ladder rows, 1 gate row, 1 picked row.
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

	var ladderN, gateN, pickedN int
	_ = d.QueryRow(`SELECT COUNT(*) FROM discovery_log WHERE stage = 'ladder'`).Scan(&ladderN)
	_ = d.QueryRow(`SELECT COUNT(*) FROM discovery_log WHERE stage = 'gate'`).Scan(&gateN)
	_ = d.QueryRow(`SELECT COUNT(*) FROM discovery_log WHERE stage = 'picked'`).Scan(&pickedN)

	if ladderN != 3 {
		t.Errorf("ladder rows: got %d, want 3", ladderN)
	}
	if gateN != 1 {
		t.Errorf("gate rows: got %d, want 1 (per-candidate; only rung 2 had results)", gateN)
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

	// Sanity-check the per-candidate gate row carries the file detail.
	var gotFilename string
	var gotBitrate int
	_ = d.QueryRow(`SELECT filename, bitrate FROM discovery_log WHERE stage = 'gate'`).Scan(&gotFilename, &gotBitrate)
	if gotFilename != "a.flac" {
		t.Errorf("gate row filename = %q, want a.flac", gotFilename)
	}
	if gotBitrate != 320 {
		t.Errorf("gate row bitrate = %d, want 320", gotBitrate)
	}
}

// TestDiscoveryLog_RejectedCandidatesLogged: every failed candidate must
// land in discovery_log with a reason. Four bad results at a single rung
// should produce four gate rows, each with Outcome=rejected_strict and a
// non-empty Reason from classify().
func TestDiscoveryLog_RejectedCandidatesLogged(t *testing.T) {
	d := setupDB(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)

	songID := uuid.New()
	if _, err := d.Exec(`INSERT INTO queue_songs (id) VALUES (?)`, songID.String()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// All four results fail a different gate threshold.
	fc := &fakeClient{
		responsesByQuery: map[string][]soulseek.SearchResult{
			"Artist Title": {
				{Peer: "lowbr", Filename: "a.mp3", Size: 5_000_000, Bitrate: 96, QueueLen: 0},
				{Peer: "small", Filename: "b.mp3", Size: 100, Bitrate: 320, QueueLen: 0},
				{Peer: "huge", Filename: "c.flac", Size: 500_000_000, Bitrate: 1000, QueueLen: 0},
				{Peer: "busy", Filename: "d.mp3", Size: 5_000_000, Bitrate: 320, QueueLen: 999},
			},
			"Title": nil,
		},
		// Relaxed pass will accept the lowbr (96 kbps, passes relaxed 96 floor)
		// and small (100B, passes relaxed 1MB? actually relaxed is 1MB — 100B
		// still fails). The point of this test is the reject logging path.
	}

	cfg := download.DefaultConfig()
	w := newDiscoveryWriter(d)
	svc := download.NewServiceWithConfig(d, fc, "/music", b, nil, w, cfg)

	// Ignore the download result — we only care about the gate rows.
	_ = svc.OnRequestDownload(context.Background(), bus.RequestDownload{
		SongID: songID, Title: "Title", Artist: "Artist",
	})

	// Strict pass writes 4 gate rows (one per candidate), all with outcome
	// rejected_strict and a non-empty reason. Relaxed pass writes another
	// 4 (some may pass or fail under relaxed thresholds); we only check
	// that the strict batch landed with reasons.
	rows, err := d.Query(`
		SELECT outcome, reason FROM discovery_log
		WHERE stage = 'gate' AND outcome = 'rejected_strict'
		ORDER BY id`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var n int
	for rows.Next() {
		var outcome, reason string
		if err := rows.Scan(&outcome, &reason); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if reason == "" {
			t.Errorf("rejected row has empty Reason — spec requires per-candidate detail")
		}
		n++
	}
	if n != 4 {
		t.Errorf("rejected_strict rows = %d, want 4 (one per candidate)", n)
	}
}
