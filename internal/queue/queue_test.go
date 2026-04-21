package queue_test

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/bus"
	"github.com/macabc/muzika/internal/db"
	"github.com/macabc/muzika/internal/queue"
)

const (
	minSize      = 5
	defaultGenre = "electronic"
)

func setupDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "muzika-test.db")
	musicDir := filepath.Join(tmp, "music")
	if err := os.MkdirAll(musicDir, 0o755); err != nil {
		t.Fatalf("mkdir music: %v", err)
	}
	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := db.MigrateEmbedded(d); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return d, musicDir
}

func seedUser(t *testing.T, d *sql.DB) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := d.Exec(
		`INSERT INTO auth_users (id, username, password) VALUES (?, ?, ?)`,
		id.String(), "u-"+id.String()[:8], "hash"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

func newService(t *testing.T) (*queue.Service, *sql.DB, string) {
	t.Helper()
	return newServiceWithDiscogs(t, true)
}

// newServiceWithDiscogs lets tests toggle the discogsEnabled flag that
// gates the legacy auto-pick Search path. Default true (most tests just
// want the method to work); TestSearch_UnavailableWhenDiscogsDisabled
// explicitly sets false.
func newServiceWithDiscogs(t *testing.T, discogsEnabled bool) (*queue.Service, *sql.DB, string) {
	t.Helper()
	d, musicDir := setupDB(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)
	svc := queue.NewServiceWithDiscogs(
		context.Background(), d, musicDir, minSize, defaultGenre,
		b, nil, discogsEnabled, 0,
	)
	return svc, d, musicDir
}

// TestRefiller_InsertsStubsAndPublishes verifies that an empty queue triggers
// exactly minSize stub inserts and DiscoveryIntent publishes.
func TestRefiller_InsertsStubsAndPublishes(t *testing.T) {
	svc, d, _ := newService(t)
	uid := seedUser(t, d)
	ctx := context.Background()

	// Subscribe BEFORE triggering so we observe the publishes.
	ch := bus.Subscribe[bus.DiscoveryIntent](busFromService(svc), "test/discovery-intent")

	svc.Refiller().Trigger(ctx, uid)

	// Collect published events with a short deadline.
	got := drain(ch, minSize, 500*time.Millisecond)
	if len(got) != minSize {
		t.Errorf("got %d published events, want %d", len(got), minSize)
	}
	for _, ev := range got {
		if ev.Strategy != bus.StrategyRandom {
			t.Errorf("event strategy %q, want %q", ev.Strategy, bus.StrategyRandom)
		}
		if ev.Genre != defaultGenre {
			t.Errorf("event genre %q, want %q", ev.Genre, defaultGenre)
		}
		if ev.UserID != uid {
			t.Errorf("event user_id %v, want %v", ev.UserID, uid)
		}
	}

	// Stub song rows should exist.
	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM queue_songs WHERE genre = ?`, defaultGenre).Scan(&n)
	if n != minSize {
		t.Errorf("expected %d stub songs, got %d", minSize, n)
	}
}

// TestRefiller_HonorsUserPreferences: v0.4.1 PR A. When a PreferredGenres
// lookup returns non-empty lists, the refiller picks genres from them
// (per source) instead of the configured default. Bandcamp-routed intents
// draw from bandcampTags; Discogs-routed from discogsGenres.
func TestRefiller_HonorsUserPreferences(t *testing.T) {
	d, musicDir := setupDB(t)
	uid := seedUser(t, d)
	ctx := context.Background()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)

	bandcampPrefs := []string{"progressive-house", "vaporwave"}
	discogsPrefs := []string{"Electronic", "Jazz"}
	lookup := func(_ context.Context, _ uuid.UUID) ([]string, []string) {
		return bandcampPrefs, discogsPrefs
	}

	// Discogs disabled → all picks routed to Bandcamp → genre must come
	// from bandcampPrefs, never the default "rock".
	svc := queue.NewServiceFull(
		ctx, d, musicDir, minSize,
		"rock",     // defaultBandcamp (must not be used)
		"Pop",      // defaultDiscogs (must not be used)
		b, nil,
		false, 0,   // discogs disabled
		lookup,
	)

	ch := bus.Subscribe[bus.DiscoveryIntent](svc.RefillerBus(), "test/prefs-bandcamp")
	svc.Refiller().Trigger(ctx, uid)
	got := drain(ch, minSize, 500*time.Millisecond)

	if len(got) == 0 {
		t.Fatal("no events published")
	}
	for _, ev := range got {
		if ev.Genre != "progressive-house" && ev.Genre != "vaporwave" {
			t.Errorf("event genre %q not from bandcamp prefs %v", ev.Genre, bandcampPrefs)
		}
	}
}

// TestRefiller_FallsBackToDefaultsWithoutPrefs: when the PreferredGenres
// lookup returns empty lists (user hasn't set any), the refiller uses
// the configured default genre.
func TestRefiller_FallsBackToDefaultsWithoutPrefs(t *testing.T) {
	d, musicDir := setupDB(t)
	uid := seedUser(t, d)
	ctx := context.Background()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)

	emptyLookup := func(_ context.Context, _ uuid.UUID) ([]string, []string) {
		return nil, nil
	}

	svc := queue.NewServiceFull(
		ctx, d, musicDir, minSize,
		"house",    // defaultBandcamp (fallback)
		"Pop",      // defaultDiscogs
		b, nil,
		false, 0,
		emptyLookup,
	)

	ch := bus.Subscribe[bus.DiscoveryIntent](svc.RefillerBus(), "test/prefs-fallback")
	svc.Refiller().Trigger(ctx, uid)
	got := drain(ch, minSize, 500*time.Millisecond)

	if len(got) == 0 {
		t.Fatal("no events published")
	}
	for _, ev := range got {
		if ev.Genre != "house" {
			t.Errorf("event genre %q, want default %q", ev.Genre, "house")
		}
	}
}

// TestRefiller_NoOpWhenQueueFull ensures we don't re-emit when queue is at threshold.
func TestRefiller_NoOpWhenQueueFull(t *testing.T) {
	svc, d, _ := newService(t)
	uid := seedUser(t, d)
	ctx := context.Background()

	// Seed queue with minSize entries.
	for i := 0; i < minSize; i++ {
		sid := uuid.New()
		if _, err := d.Exec(`INSERT INTO queue_songs (id) VALUES (?)`, sid.String()); err != nil {
			t.Fatalf("seed song: %v", err)
		}
		if _, err := d.Exec(
			`INSERT INTO queue_entries (id, user_id, song_id, position) VALUES (?, ?, ?, ?)`,
			uuid.NewString(), uid.String(), sid.String(), i); err != nil {
			t.Fatalf("seed entry: %v", err)
		}
	}

	ch := bus.Subscribe[bus.DiscoveryIntent](busFromService(svc), "test/no-op")
	svc.Refiller().Trigger(ctx, uid)

	got := drain(ch, 1, 150*time.Millisecond)
	if len(got) != 0 {
		t.Errorf("expected 0 published events, got %d", len(got))
	}
}

// TestOnLoadedSong_CompletedAppendsToQueue verifies that a COMPLETED event
// attaches the song to the stub's requesting_user_id's queue.
func TestOnLoadedSong_CompletedAppendsToQueue(t *testing.T) {
	svc, d, _ := newService(t)
	uid := seedUser(t, d)
	ctx := context.Background()

	sid := uuid.New()
	// Seed with requesting_user_id so onLoadedSong knows which queue to touch.
	if _, err := d.Exec(
		`INSERT INTO queue_songs (id, genre, requesting_user_id) VALUES (?, ?, ?)`,
		sid.String(), defaultGenre, uid.String()); err != nil {
		t.Fatalf("seed song: %v", err)
	}

	err := svc.OnLoadedSong(ctx, bus.LoadedSong{
		SongID:   sid,
		FilePath: "some-file.mp3",
		Status:   bus.LoadedStatusCompleted,
	})
	if err != nil {
		t.Fatalf("onLoadedSong: %v", err)
	}

	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM queue_entries WHERE user_id = ? AND song_id = ?`,
		uid.String(), sid.String()).Scan(&n)
	if n != 1 {
		t.Errorf("expected queue entry, got count=%d", n)
	}

	// URL stored on the song row.
	var url sql.NullString
	_ = d.QueryRow(`SELECT url FROM queue_songs WHERE id = ?`, sid.String()).Scan(&url)
	if !url.Valid || url.String != "some-file.mp3" {
		t.Errorf("url mismatch: %+v", url)
	}
}

// TestOnLoadedSong_PerUserIsolation: user B's completed download must NOT
// land in user A's queue. Regression test for the old appendToShortQueues
// behavior where any short queue picked up any completed download.
func TestOnLoadedSong_PerUserIsolation(t *testing.T) {
	svc, d, _ := newService(t)
	userA := seedUser(t, d)
	userB := seedUser(t, d)
	ctx := context.Background()

	// Stub is owned by user B.
	sid := uuid.New()
	if _, err := d.Exec(
		`INSERT INTO queue_songs (id, genre, requesting_user_id) VALUES (?, ?, ?)`,
		sid.String(), defaultGenre, userB.String()); err != nil {
		t.Fatalf("seed song: %v", err)
	}

	if err := svc.OnLoadedSong(ctx, bus.LoadedSong{
		SongID:   sid,
		FilePath: "b-only.mp3",
		Status:   bus.LoadedStatusCompleted,
	}); err != nil {
		t.Fatalf("onLoadedSong: %v", err)
	}

	// User B gets the entry.
	var nB int
	_ = d.QueryRow(`SELECT COUNT(*) FROM queue_entries WHERE user_id = ? AND song_id = ?`,
		userB.String(), sid.String()).Scan(&nB)
	if nB != 1 {
		t.Errorf("user B expected 1 queue entry, got %d", nB)
	}

	// User A must NOT get the entry.
	var nA int
	_ = d.QueryRow(`SELECT COUNT(*) FROM queue_entries WHERE user_id = ? AND song_id = ?`,
		userA.String(), sid.String()).Scan(&nA)
	if nA != 0 {
		t.Errorf("user A queue leaked B's download; got %d entries", nA)
	}
}

// TestOnLoadedSong_NoRequesterIsNoop: a stub without a requester (e.g. legacy
// row) does not crash and does not attach to anyone.
func TestOnLoadedSong_NoRequesterIsNoop(t *testing.T) {
	svc, d, _ := newService(t)
	_ = seedUser(t, d) // user exists but is not the requester
	ctx := context.Background()

	sid := uuid.New()
	if _, err := d.Exec(`INSERT INTO queue_songs (id, genre) VALUES (?, ?)`,
		sid.String(), defaultGenre); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := svc.OnLoadedSong(ctx, bus.LoadedSong{
		SongID:   sid,
		FilePath: "orphan.mp3",
		Status:   bus.LoadedStatusCompleted,
	}); err != nil {
		t.Fatalf("onLoadedSong: %v", err)
	}

	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM queue_entries WHERE song_id = ?`, sid.String()).Scan(&n)
	if n != 0 {
		t.Errorf("orphan stub leaked into queues; got %d entries", n)
	}
}

// TestOnLoadedSong_ErrorDeletesStub verifies the ERROR path.
func TestOnLoadedSong_ErrorDeletesStub(t *testing.T) {
	svc, d, _ := newService(t)
	ctx := context.Background()

	sid := uuid.New()
	if _, err := d.Exec(`INSERT INTO queue_songs (id) VALUES (?)`, sid.String()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	err := svc.OnLoadedSong(ctx, bus.LoadedSong{
		SongID: sid,
		Status: bus.LoadedStatusError,
	})
	if err != nil {
		t.Fatalf("onLoadedSong: %v", err)
	}

	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM queue_songs WHERE id = ?`, sid.String()).Scan(&n)
	if n != 0 {
		t.Errorf("expected stub deleted, count=%d", n)
	}
}

// TestSearch_NormalizesAndPublishesDiscoveryIntent: end-to-end for the
// user-initiated search path (v0.4 PR 3). Feeds a messy query, asserts the
// service normalizes it, inserts a stub, and emits DiscoveryIntent with
// StrategySearch + PreferredSources=["discogs"].
func TestSearch_NormalizesAndPublishesDiscoveryIntent(t *testing.T) {
	svc, d, _ := newService(t)
	uid := seedUser(t, d)
	ctx := context.Background()

	ch := bus.Subscribe[bus.DiscoveryIntent](busFromService(svc), "test/search")

	resp, err := svc.Search(ctx, uid, queue.SearchRequest{Query: "  Boards OF Canada — (1998)  "})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if resp.Query != "boards of canada 1998" {
		t.Errorf("normalized query = %q, want %q", resp.Query, "boards of canada 1998")
	}

	got := drain(ch, 1, 500*time.Millisecond)
	if len(got) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(got))
	}
	ev := got[0]
	if ev.Strategy != bus.StrategySearch {
		t.Errorf("strategy %q, want %q", ev.Strategy, bus.StrategySearch)
	}
	if ev.Query != "boards of canada 1998" {
		t.Errorf("query %q in event, want normalized form", ev.Query)
	}
	if len(ev.PreferredSources) != 1 || ev.PreferredSources[0] != "discogs" {
		t.Errorf("PreferredSources %v, want [discogs]", ev.PreferredSources)
	}
	if ev.SongID != resp.SongID {
		t.Errorf("event SongID %v, want %v", ev.SongID, resp.SongID)
	}
	if ev.UserID != uid {
		t.Errorf("event UserID %v, want %v", ev.UserID, uid)
	}

	// Stub row exists with requesting_user_id = uid.
	var reqStr string
	_ = d.QueryRow(`SELECT requesting_user_id FROM queue_songs WHERE id = ?`,
		resp.SongID.String()).Scan(&reqStr)
	if reqStr != uid.String() {
		t.Errorf("stub requester = %q, want %q", reqStr, uid.String())
	}
}

// TestSearch_EmptyAfterNormalization: punctuation-only queries return
// ErrEmptyQuery and no stub is inserted.
func TestSearch_EmptyAfterNormalization(t *testing.T) {
	svc, d, _ := newService(t)
	uid := seedUser(t, d)
	ctx := context.Background()

	_, err := svc.Search(ctx, uid, queue.SearchRequest{Query: "!!! ??? ..."})
	if err != queue.ErrEmptyQuery {
		t.Errorf("got %v, want ErrEmptyQuery", err)
	}

	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM queue_songs`).Scan(&n)
	if n != 0 {
		t.Errorf("stub should not have been inserted, count=%d", n)
	}
}

// TestSearch_UnavailableWhenDiscogsDisabled: v0.4.1 PR C Bug #1. The
// legacy auto-pick Search path used to silently leak a stub when Discogs
// was off (no subscriber for DiscoveryIntent{StrategySearch}). Now
// returns ErrSearchUnavailable before inserting anything.
func TestSearch_UnavailableWhenDiscogsDisabled(t *testing.T) {
	svc, d, _ := newServiceWithDiscogs(t, false)
	uid := seedUser(t, d)
	ctx := context.Background()

	_, err := svc.Search(ctx, uid, queue.SearchRequest{Query: "anything"})
	if err != queue.ErrSearchUnavailable {
		t.Errorf("got %v, want ErrSearchUnavailable", err)
	}
	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM queue_songs`).Scan(&n)
	if n != 0 {
		t.Errorf("stub inserted despite disabled Discogs: %d", n)
	}
}

// TestSearch_PrePickedEmitsRequestDownloadDirectly: v0.4.1 PR C. When
// the request carries metadata (user picked from the preview dropdown),
// Search skips the DiscoveryIntent → Discogs fan-out and publishes a
// RequestDownload directly. Works even with Discogs disabled — the
// Discogs API call already happened during preview.
func TestSearch_PrePickedEmitsRequestDownloadDirectly(t *testing.T) {
	svc, d, _ := newServiceWithDiscogs(t, false) // discogs off intentionally
	uid := seedUser(t, d)
	ctx := context.Background()

	ch := bus.Subscribe[bus.RequestDownload](svc.RefillerBus(), "test/pre-picked")

	resp, err := svc.Search(ctx, uid, queue.SearchRequest{
		Query:         "shanti people",
		Title:         "Saraswati",
		Artist:        "Shanti People",
		CatalogNumber: "PAR-001",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	got := drain(ch, 1, 500*time.Millisecond)
	if len(got) != 1 {
		t.Fatalf("expected 1 RequestDownload, got %d", len(got))
	}
	ev := got[0]
	if ev.SongID != resp.SongID {
		t.Errorf("event SongID %v, want %v", ev.SongID, resp.SongID)
	}
	if ev.Title != "Saraswati" || ev.Artist != "Shanti People" {
		t.Errorf("event metadata: %+v", ev)
	}
	if ev.CatalogNumber != "PAR-001" {
		t.Errorf("event catno = %q, want PAR-001", ev.CatalogNumber)
	}
	if ev.Strategy != bus.StrategySearch {
		t.Errorf("event strategy = %q, want StrategySearch", ev.Strategy)
	}

	// Stub has metadata populated synchronously so the UI sees artist/title
	// the moment the search returns.
	var title, artist sql.NullString
	_ = d.QueryRow(`SELECT title, artist FROM queue_songs WHERE id = ?`, resp.SongID.String()).Scan(&title, &artist)
	if !title.Valid || title.String != "Saraswati" {
		t.Errorf("stub title = %v, want Saraswati", title)
	}
	if !artist.Valid || artist.String != "Shanti People" {
		t.Errorf("stub artist = %v, want Shanti People", artist)
	}
}

// TestOnLoadedSong_RelaxedSurfacedForSearchPath: a LoadedSong with
// Relaxed=true lands queue_entries.relaxed=1 and the DTO exposes it.
func TestOnLoadedSong_RelaxedSurfacedForSearchPath(t *testing.T) {
	svc, d, _ := newService(t)
	uid := seedUser(t, d)
	ctx := context.Background()

	sid := uuid.New()
	if _, err := d.Exec(
		`INSERT INTO queue_songs (id, genre, requesting_user_id) VALUES (?, ?, ?)`,
		sid.String(), defaultGenre, uid.String()); err != nil {
		t.Fatalf("seed song: %v", err)
	}

	if err := svc.OnLoadedSong(ctx, bus.LoadedSong{
		SongID:   sid,
		FilePath: "relaxed.mp3",
		Status:   bus.LoadedStatusCompleted,
		Relaxed:  true,
	}); err != nil {
		t.Fatalf("onLoadedSong: %v", err)
	}

	var relaxed int
	_ = d.QueryRow(`SELECT relaxed FROM queue_entries WHERE user_id = ? AND song_id = ?`,
		uid.String(), sid.String()).Scan(&relaxed)
	if relaxed != 1 {
		t.Errorf("queue_entries.relaxed = %d, want 1", relaxed)
	}

	resp, err := svc.GetQueue(ctx, uid)
	if err != nil {
		t.Fatalf("GetQueue: %v", err)
	}
	if len(resp.Songs) != 1 || !resp.Songs[0].Relaxed {
		t.Errorf("DTO relaxed flag not surfaced: %+v", resp.Songs)
	}
}

// TestOnLoadedSong_NotRelaxedForPassivePath: Relaxed=false keeps
// queue_entries.relaxed at 0. This is the passive-refill default —
// the download worker only sets Relaxed=true when the origin was
// user-initiated search (ROADMAP §v0.4 item 6).
func TestOnLoadedSong_NotRelaxedForPassivePath(t *testing.T) {
	svc, d, _ := newService(t)
	uid := seedUser(t, d)
	ctx := context.Background()

	sid := uuid.New()
	if _, err := d.Exec(
		`INSERT INTO queue_songs (id, genre, requesting_user_id) VALUES (?, ?, ?)`,
		sid.String(), defaultGenre, uid.String()); err != nil {
		t.Fatalf("seed song: %v", err)
	}

	if err := svc.OnLoadedSong(ctx, bus.LoadedSong{
		SongID:   sid,
		FilePath: "passive.mp3",
		Status:   bus.LoadedStatusCompleted,
		Relaxed:  false,
	}); err != nil {
		t.Fatalf("onLoadedSong: %v", err)
	}

	var relaxed int
	_ = d.QueryRow(`SELECT relaxed FROM queue_entries WHERE user_id = ? AND song_id = ?`,
		uid.String(), sid.String()).Scan(&relaxed)
	if relaxed != 0 {
		t.Errorf("queue_entries.relaxed = %d, want 0", relaxed)
	}
}

// TestOnRequestDownload_SearchIntentInsertsProbingEntry: v0.4.1 PR B. A
// RequestDownload carrying Strategy=StrategySearch inserts a queue_entries
// row with status='probing' for the requester. Passive refill intents
// (StrategyRandom) do NOT insert an entry here — that happens later in
// onLoadedSong.
func TestOnRequestDownload_SearchIntentInsertsProbingEntry(t *testing.T) {
	svc, d, _ := newService(t)
	uid := seedUser(t, d)
	ctx := context.Background()

	sid := uuid.New()
	if _, err := d.Exec(
		`INSERT INTO queue_songs (id, requesting_user_id) VALUES (?, ?)`,
		sid.String(), uid.String()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	err := svc.OnRequestDownload(ctx, bus.RequestDownload{
		SongID: sid, Title: "A Title", Artist: "An Artist",
		Strategy: bus.StrategySearch,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	var status string
	_ = d.QueryRow(`SELECT status FROM queue_entries WHERE user_id = ? AND song_id = ?`,
		uid.String(), sid.String()).Scan(&status)
	if status != "probing" {
		t.Errorf("got status %q, want 'probing'", status)
	}
}

func TestOnRequestDownload_PassiveIntentDoesNotInsertEntry(t *testing.T) {
	svc, d, _ := newService(t)
	uid := seedUser(t, d)
	ctx := context.Background()

	sid := uuid.New()
	if _, err := d.Exec(
		`INSERT INTO queue_songs (id, requesting_user_id) VALUES (?, ?)`,
		sid.String(), uid.String()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	err := svc.OnRequestDownload(ctx, bus.RequestDownload{
		SongID: sid, Title: "T", Artist: "A",
		Strategy: bus.StrategyRandom,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM queue_entries WHERE user_id = ? AND song_id = ?`,
		uid.String(), sid.String()).Scan(&n)
	if n != 0 {
		t.Errorf("passive intent should not insert entry; got %d", n)
	}
}

// TestOnLoadedSong_NotFoundDeletesStub: v0.4.2 PR A flipped the v0.4.1
// PR B behavior. NotFound now deletes the stub (and cascades to the
// queue_entries row) rather than marking status='not_found'. This
// prevents the "in queue AND not available" paradox the user hit when
// a re-search's probe failed despite an already-ready copy existing.
func TestOnLoadedSong_NotFoundDeletesStub(t *testing.T) {
	svc, d, _ := newService(t)
	uid := seedUser(t, d)
	ctx := context.Background()

	sid := uuid.New()
	if _, err := d.Exec(
		`INSERT INTO queue_songs (id, requesting_user_id) VALUES (?, ?)`,
		sid.String(), uid.String()); err != nil {
		t.Fatalf("seed song: %v", err)
	}
	if _, err := d.Exec(
		`INSERT INTO queue_entries (id, user_id, song_id, position, status)
		 VALUES (?, ?, ?, 0, 'probing')`,
		uuid.NewString(), uid.String(), sid.String()); err != nil {
		t.Fatalf("seed entry: %v", err)
	}

	err := svc.OnLoadedSong(ctx, bus.LoadedSong{
		SongID: sid,
		Status: bus.LoadedStatusNotFound,
	})
	if err != nil {
		t.Fatalf("onLoadedSong: %v", err)
	}

	var songN int
	_ = d.QueryRow(`SELECT COUNT(*) FROM queue_songs WHERE id = ?`, sid.String()).Scan(&songN)
	if songN != 0 {
		t.Errorf("stub should be deleted on NotFound, count=%d", songN)
	}
	var entryN int
	_ = d.QueryRow(`SELECT COUNT(*) FROM queue_entries WHERE song_id = ?`, sid.String()).Scan(&entryN)
	if entryN != 0 {
		t.Errorf("queue_entries row should cascade-delete, count=%d", entryN)
	}
}

// TestSearch_CacheHitSkipsSoulseekWhenFileOnDisk: v0.4.2 PR A.1. If
// a queue_songs row for this (title, artist) exists AND its url
// resolves to a file actually present on disk, re-search is a catalog
// hit: append a ready queue_entries row for the user, return the
// existing SongID, emit ZERO RequestDownload events. No need to bother
// Soulseek — we already have the file.
//
// Fixes: "songs are not found on Soulseek even though we downloaded them."
func TestSearch_CacheHitSkipsSoulseekWhenFileOnDisk(t *testing.T) {
	svc, d, musicDir := newService(t)
	uid := seedUser(t, d)
	ctx := context.Background()

	// Drop a real file into the music dir so the disk check passes.
	relPath := "sha-mo-3000-test.mp3"
	absPath := filepath.Join(musicDir, relPath)
	if err := os.WriteFile(absPath, []byte("fake audio"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	existing := uuid.New()
	if _, err := d.Exec(
		`INSERT INTO queue_songs (id, title, artist, url, requesting_user_id)
		 VALUES (?, ?, ?, ?, ?)`,
		existing.String(), "Sha Mo 3000", "Merzbow", relPath, uid.String()); err != nil {
		t.Fatalf("seed song: %v", err)
	}

	ch := bus.Subscribe[bus.RequestDownload](svc.RefillerBus(), "test/cache-hit")

	resp, err := svc.Search(ctx, uid, queue.SearchRequest{
		// Different casing on purpose — cache hit is case-insensitive.
		Title:  "sha mo 3000",
		Artist: "MERZBOW",
		Query:  "sha mo 3000",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if resp.SongID != existing {
		t.Errorf("got SongID %v, want existing %v (cache hit should reuse)", resp.SongID, existing)
	}

	// Zero RequestDownloads — we already have the file.
	got := drain(ch, 1, 150*time.Millisecond)
	if len(got) != 0 {
		t.Errorf("cache-hit path fired RequestDownload: %+v", got)
	}

	// A ready queue_entries row exists for this user + song.
	var status string
	if err := d.QueryRow(
		`SELECT status FROM queue_entries WHERE user_id = ? AND song_id = ?`,
		uid.String(), existing.String()).Scan(&status); err != nil {
		t.Fatalf("select entry: %v", err)
	}
	if status != "ready" {
		t.Errorf("entry status=%q, want 'ready'", status)
	}

	// No duplicate queue_songs row was inserted.
	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM queue_songs`).Scan(&n)
	if n != 1 {
		t.Errorf("duplicate stub inserted, count=%d", n)
	}
}

// TestSearch_ReuseStubIdWhenFileMissing: v0.4.2 PR A.1. If a
// queue_songs row exists but its url is set to a path that DOESN'T
// resolve to a file (deleted out-of-band), search-acquire reuses the
// same SongID + re-emits RequestDownload so the download worker pulls
// the file again into the SAME catalog row. No duplicate rows.
func TestSearch_ReuseStubIdWhenFileMissing(t *testing.T) {
	svc, d, _ := newService(t)
	uid := seedUser(t, d)
	ctx := context.Background()

	existing := uuid.New()
	if _, err := d.Exec(
		`INSERT INTO queue_songs (id, title, artist, url, requesting_user_id)
		 VALUES (?, ?, ?, ?, ?)`,
		existing.String(), "Proper Lady", "Erez", "gone-from-disk.mp3", uid.String()); err != nil {
		t.Fatalf("seed song: %v", err)
	}
	// Intentionally do NOT create the file at gone-from-disk.mp3.

	ch := bus.Subscribe[bus.RequestDownload](svc.RefillerBus(), "test/reuse-missing-file")

	resp, err := svc.Search(ctx, uid, queue.SearchRequest{
		Title:  "Proper Lady",
		Artist: "Erez",
		Query:  "proper lady",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if resp.SongID != existing {
		t.Errorf("got SongID %v, want existing %v (reuse path)", resp.SongID, existing)
	}

	got := drain(ch, 1, 500*time.Millisecond)
	if len(got) != 1 {
		t.Fatalf("expected one RequestDownload (reuse triggers re-probe), got %d", len(got))
	}
	if got[0].SongID != existing {
		t.Errorf("RequestDownload SongID = %v, want %v", got[0].SongID, existing)
	}

	// queue_songs count still 1 — reuse, not insert.
	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM queue_songs`).Scan(&n)
	if n != 1 {
		t.Errorf("duplicate stub inserted despite reuse path, count=%d", n)
	}
}

// TestSearch_ReuseStubIdUpdatesRequester: when user B searches for a
// song that user A originally stamped (in the queue_songs row), the
// requesting_user_id gets flipped to user B so onRequestDownload's
// probing insert and onLoadedSong's promote attribute correctly.
func TestSearch_ReuseStubIdUpdatesRequester(t *testing.T) {
	svc, d, _ := newService(t)
	userA := seedUser(t, d)
	userB := seedUser(t, d)
	ctx := context.Background()

	existing := uuid.New()
	if _, err := d.Exec(
		`INSERT INTO queue_songs (id, title, artist, requesting_user_id)
		 VALUES (?, ?, ?, ?)`,
		existing.String(), "Ambient 1", "Brian Eno", userA.String()); err != nil {
		t.Fatalf("seed song: %v", err)
	}

	_, err := svc.Search(ctx, userB, queue.SearchRequest{
		Title: "Ambient 1", Artist: "Brian Eno", Query: "ambient 1",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	var reqStr string
	_ = d.QueryRow(`SELECT requesting_user_id FROM queue_songs WHERE id = ?`,
		existing.String()).Scan(&reqStr)
	if reqStr != userB.String() {
		t.Errorf("requesting_user_id = %q, want user B = %q", reqStr, userB.String())
	}
}

// TestOnLoadedSong_CompletedPromotesProbingToReady: when a probing entry
// exists (from a search intent), Completed promotes it rather than
// inserting a duplicate.
func TestOnLoadedSong_CompletedPromotesProbingToReady(t *testing.T) {
	svc, d, _ := newService(t)
	uid := seedUser(t, d)
	ctx := context.Background()

	sid := uuid.New()
	if _, err := d.Exec(
		`INSERT INTO queue_songs (id, requesting_user_id) VALUES (?, ?)`,
		sid.String(), uid.String()); err != nil {
		t.Fatalf("seed song: %v", err)
	}
	if _, err := d.Exec(
		`INSERT INTO queue_entries (id, user_id, song_id, position, status)
		 VALUES (?, ?, ?, 0, 'probing')`,
		uuid.NewString(), uid.String(), sid.String()); err != nil {
		t.Fatalf("seed entry: %v", err)
	}

	err := svc.OnLoadedSong(ctx, bus.LoadedSong{
		SongID:   sid,
		FilePath: "song.mp3",
		Status:   bus.LoadedStatusCompleted,
	})
	if err != nil {
		t.Fatalf("onLoadedSong: %v", err)
	}

	// Single entry, now ready.
	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM queue_entries WHERE user_id = ? AND song_id = ?`,
		uid.String(), sid.String()).Scan(&n)
	if n != 1 {
		t.Errorf("expected 1 entry (promoted), got %d", n)
	}
	var status string
	_ = d.QueryRow(`SELECT status FROM queue_entries WHERE user_id = ? AND song_id = ?`,
		uid.String(), sid.String()).Scan(&status)
	if status != "ready" {
		t.Errorf("got status %q, want 'ready'", status)
	}
}

func TestOnRequestDownload_UpdatesMetadata(t *testing.T) {
	svc, d, _ := newService(t)
	ctx := context.Background()

	sid := uuid.New()
	if _, err := d.Exec(`INSERT INTO queue_songs (id) VALUES (?)`, sid.String()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	err := svc.OnRequestDownload(ctx, bus.RequestDownload{
		SongID: sid, Title: "Test Title", Artist: "Test Artist",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	var title, artist sql.NullString
	_ = d.QueryRow(`SELECT title, artist FROM queue_songs WHERE id = ?`, sid.String()).Scan(&title, &artist)
	if !title.Valid || title.String != "Test Title" {
		t.Errorf("title mismatch: %+v", title)
	}
	if !artist.Valid || artist.String != "Test Artist" {
		t.Errorf("artist mismatch: %+v", artist)
	}
}

// TestLike_EmitsLikedSongOutbox checks the outbox row and DB state.
func TestLike_EmitsLikedSongOutbox(t *testing.T) {
	svc, d, _ := newService(t)
	uid := seedUser(t, d)
	ctx := context.Background()

	sid := uuid.New()
	if _, err := d.Exec(`INSERT INTO queue_songs (id) VALUES (?)`, sid.String()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := svc.Like(ctx, uid, sid); err != nil {
		t.Fatalf("Like: %v", err)
	}

	var liked int
	_ = d.QueryRow(`SELECT liked FROM queue_user_songs WHERE user_id = ? AND song_id = ?`,
		uid.String(), sid.String()).Scan(&liked)
	if liked != 1 {
		t.Errorf("liked flag mismatch: %d", liked)
	}
	var outboxCount int
	_ = d.QueryRow(`SELECT COUNT(*) FROM outbox WHERE event_type = ?`, bus.TypeLikedSong).Scan(&outboxCount)
	if outboxCount != 1 {
		t.Errorf("expected 1 LikedSong outbox row, got %d", outboxCount)
	}
}

func TestUnlike_EmitsUnlikedSongOutbox(t *testing.T) {
	svc, d, _ := newService(t)
	uid := seedUser(t, d)
	ctx := context.Background()

	sid := uuid.New()
	if _, err := d.Exec(`INSERT INTO queue_songs (id) VALUES (?)`, sid.String()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := svc.Unlike(ctx, uid, sid); err != nil {
		t.Fatalf("Unlike: %v", err)
	}

	var outboxCount int
	_ = d.QueryRow(`SELECT COUNT(*) FROM outbox WHERE event_type = ?`, bus.TypeUnlikedSong).Scan(&outboxCount)
	if outboxCount != 1 {
		t.Errorf("expected 1 UnlikedSong outbox row, got %d", outboxCount)
	}
}

// TestMarkSkipped_SetsSkippedAndRemovesEntry verifies Bug 3's fix:
// the skipped-flag upsert and the queue-entry delete land together, under
// the per-user lock.
func TestMarkSkipped_SetsSkippedAndRemovesEntry(t *testing.T) {
	svc, d, _ := newService(t)
	uid := seedUser(t, d)
	ctx := context.Background()

	sid := uuid.New()
	if _, err := d.Exec(`INSERT INTO queue_songs (id) VALUES (?)`, sid.String()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := d.Exec(
		`INSERT INTO queue_entries (id, user_id, song_id, position) VALUES (?, ?, ?, 0)`,
		uuid.NewString(), uid.String(), sid.String()); err != nil {
		t.Fatalf("seed entry: %v", err)
	}

	if err := svc.MarkSkipped(ctx, uid, queue.SongIDRequest{SongID: sid}); err != nil {
		t.Fatalf("MarkSkipped: %v", err)
	}

	// Skipped flag set.
	var skipped int
	_ = d.QueryRow(
		`SELECT skipped FROM queue_user_songs WHERE user_id = ? AND song_id = ?`,
		uid.String(), sid.String()).Scan(&skipped)
	if skipped != 1 {
		t.Errorf("skipped=%d, want 1", skipped)
	}
	// Queue entry removed.
	var inQueue int
	_ = d.QueryRow(`SELECT COUNT(*) FROM queue_entries WHERE user_id = ? AND song_id = ?`,
		uid.String(), sid.String()).Scan(&inQueue)
	if inQueue != 0 {
		t.Errorf("queue entry should be removed, count=%d", inQueue)
	}
}

// TestMarkSkipped_NoEntryStillMarksSkipped documents the tolerated-edge:
// if the queue_entry is already gone (e.g. a concurrent MarkFinished landed
// first), MarkSkipped still persists the skipped flag rather than returning
// ErrNotFound. Ensures future refactors keep the errors.Is(err, ErrNotFound)
// branch in MarkSkipped.
func TestMarkSkipped_NoEntryStillMarksSkipped(t *testing.T) {
	svc, d, _ := newService(t)
	uid := seedUser(t, d)
	ctx := context.Background()

	sid := uuid.New()
	if _, err := d.Exec(`INSERT INTO queue_songs (id) VALUES (?)`, sid.String()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// No queue_entries row — the song isn't in this user's queue.

	if err := svc.MarkSkipped(ctx, uid, queue.SongIDRequest{SongID: sid}); err != nil {
		t.Fatalf("MarkSkipped: %v", err)
	}

	var skipped int
	_ = d.QueryRow(
		`SELECT skipped FROM queue_user_songs WHERE user_id = ? AND song_id = ?`,
		uid.String(), sid.String()).Scan(&skipped)
	if skipped != 1 {
		t.Errorf("skipped=%d, want 1", skipped)
	}
}

// TestMarkSkipped_RollsBackOnFailure verifies the tx wraps both mutations:
// if the first mutation fails (FK violation — song_id not in queue_songs),
// the queue_entry is NOT removed. Both-or-neither.
func TestMarkSkipped_RollsBackOnFailure(t *testing.T) {
	svc, d, _ := newService(t)
	uid := seedUser(t, d)
	ctx := context.Background()

	// Seed a real song + queue_entry pair.
	realSID := uuid.New()
	if _, err := d.Exec(`INSERT INTO queue_songs (id) VALUES (?)`, realSID.String()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := d.Exec(
		`INSERT INTO queue_entries (id, user_id, song_id, position) VALUES (?, ?, ?, 0)`,
		uuid.NewString(), uid.String(), realSID.String()); err != nil {
		t.Fatalf("seed entry: %v", err)
	}

	// Now call MarkSkipped with a bogus song_id — the INSERT into
	// queue_user_songs will fail FK. The real queue_entry must survive
	// (nothing else changed inside the tx, but the important property is
	// that MarkSkipped returns an error rather than partial success).
	bogus := uuid.New()
	err := svc.MarkSkipped(ctx, uid, queue.SongIDRequest{SongID: bogus})
	if err == nil {
		t.Fatal("expected error from FK violation, got nil")
	}

	// The real queue_entry is still there (the failed tx touched nothing).
	var inQueue int
	_ = d.QueryRow(`SELECT COUNT(*) FROM queue_entries WHERE user_id = ? AND song_id = ?`,
		uid.String(), realSID.String()).Scan(&inQueue)
	if inQueue != 1 {
		t.Errorf("real queue entry disappeared after unrelated failure, count=%d", inQueue)
	}
	// No listen_count / skipped row was created for the bogus id.
	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM queue_user_songs WHERE song_id = ?`, bogus.String()).Scan(&n)
	if n != 0 {
		t.Errorf("partial write after FK failure: %d queue_user_songs row(s) for bogus id", n)
	}
}

func TestMarkFinished_IncrementsAndRemoves(t *testing.T) {
	svc, d, _ := newService(t)
	uid := seedUser(t, d)
	ctx := context.Background()

	sid := uuid.New()
	if _, err := d.Exec(`INSERT INTO queue_songs (id) VALUES (?)`, sid.String()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := d.Exec(
		`INSERT INTO queue_entries (id, user_id, song_id, position) VALUES (?, ?, ?, 0)`,
		uuid.NewString(), uid.String(), sid.String()); err != nil {
		t.Fatalf("seed entry: %v", err)
	}

	if err := svc.MarkFinished(ctx, uid, queue.SongIDRequest{SongID: sid}); err != nil {
		t.Fatalf("MarkFinished: %v", err)
	}

	var listens int
	_ = d.QueryRow(
		`SELECT listen_count FROM queue_user_songs WHERE user_id = ? AND song_id = ?`,
		uid.String(), sid.String()).Scan(&listens)
	if listens != 1 {
		t.Errorf("listen_count=%d, want 1", listens)
	}
	var inQueue int
	_ = d.QueryRow(`SELECT COUNT(*) FROM queue_entries WHERE user_id = ? AND song_id = ?`,
		uid.String(), sid.String()).Scan(&inQueue)
	if inQueue != 0 {
		t.Errorf("queue entry should be removed, count=%d", inQueue)
	}
}

// TestResolveSongPath verifies absolute-vs-relative URL handling.
func TestResolveSongPath(t *testing.T) {
	svc, d, musicDir := newService(t)
	ctx := context.Background()

	// Case 1: relative URL → joined with MusicStoragePath.
	sid := uuid.New()
	if _, err := d.Exec(`INSERT INTO queue_songs (id, url) VALUES (?, ?)`,
		sid.String(), "song.mp3"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err := svc.ResolveSongPath(ctx, sid)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != filepath.Join(musicDir, "song.mp3") {
		t.Errorf("got %q, want relative-joined path", got)
	}

	// Case 2: absolute URL stays absolute.
	sid2 := uuid.New()
	abs := "/tmp/absolute.mp3"
	if _, err := d.Exec(`INSERT INTO queue_songs (id, url) VALUES (?, ?)`,
		sid2.String(), abs); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err = svc.ResolveSongPath(ctx, sid2)
	if err != nil {
		t.Fatalf("resolve abs: %v", err)
	}
	if got != abs {
		t.Errorf("got %q, want %q", got, abs)
	}

	// Case 3: no URL → ErrNoFile.
	sid3 := uuid.New()
	if _, err := d.Exec(`INSERT INTO queue_songs (id) VALUES (?)`, sid3.String()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := svc.ResolveSongPath(ctx, sid3); err != queue.ErrNoFile {
		t.Errorf("got %v, want ErrNoFile", err)
	}
}

// ---- helpers ----

// busFromService extracts *bus.Bus from a Service using the refiller's field.
// Services keep a reference for testing via Refiller() and we stash the bus
// there by convention; expose via a hidden accessor.
func busFromService(svc *queue.Service) *bus.Bus {
	return svc.RefillerBus()
}

// drain reads up to want events from ch, waiting at most timeout.
func drain[T any](ch <-chan T, want int, timeout time.Duration) []T {
	deadline := time.After(timeout)
	var got []T
	for len(got) < want {
		select {
		case ev := <-ch:
			got = append(got, ev)
		case <-deadline:
			return got
		}
	}
	return got
}
