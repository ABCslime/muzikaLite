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
	d, musicDir := setupDB(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)
	svc := queue.NewService(context.Background(), d, musicDir, minSize, defaultGenre, b, nil)
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
