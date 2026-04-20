package playlist_test

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/bus"
	"github.com/macabc/muzika/internal/db"
	"github.com/macabc/muzika/internal/playlist"
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

// seedUser inserts a row directly into auth_users for FK satisfaction.
// The full auth service is tested separately; here we just need a valid user_id.
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

// seedSong inserts a minimal row into queue_songs so playlist_songs FK holds.
func seedSong(t *testing.T, d *sql.DB) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := d.Exec(
		`INSERT INTO queue_songs (id, title, artist) VALUES (?, ?, ?)`,
		id.String(), "Test Title", "Test Artist"); err != nil {
		t.Fatalf("seed song: %v", err)
	}
	return id
}

func newService(t *testing.T) (*playlist.Service, *sql.DB) {
	t.Helper()
	d := setupDB(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(64, log)
	return playlist.NewService(d, b), d
}

// TestOnUserCreated_CreatesLikedPlaylist verifies the UserCreated handler
// auto-provisions a system-liked playlist.
func TestOnUserCreated_CreatesLikedPlaylist(t *testing.T) {
	svc, d := newService(t)
	uid := seedUser(t, d)
	ctx := context.Background()

	if err := svc.OnUserCreated(ctx, bus.UserCreated{UserID: uid, Username: "test"}); err != nil {
		t.Fatalf("onUserCreated: %v", err)
	}

	var count int
	_ = d.QueryRow(`SELECT COUNT(*) FROM playlist_playlists WHERE user_id = ? AND is_system_liked = 1`,
		uid.String()).Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 system-liked playlist, got %d", count)
	}
}

func TestOnUserCreated_Idempotent(t *testing.T) {
	svc, d := newService(t)
	uid := seedUser(t, d)
	ctx := context.Background()

	// Fire twice — partial unique index should prevent duplicates.
	for i := 0; i < 2; i++ {
		if err := svc.OnUserCreated(ctx, bus.UserCreated{UserID: uid}); err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
	}
	var count int
	_ = d.QueryRow(`SELECT COUNT(*) FROM playlist_playlists WHERE user_id = ?`, uid.String()).Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 playlist, got %d", count)
	}
}

func TestOnLiked_AddsToSystemLiked(t *testing.T) {
	svc, d := newService(t)
	uid := seedUser(t, d)
	sid := seedSong(t, d)
	ctx := context.Background()

	if err := svc.OnLiked(ctx, bus.LikedSong{UserID: uid, SongID: sid}); err != nil {
		t.Fatalf("onLiked: %v", err)
	}

	var count int
	_ = d.QueryRow(`
		SELECT COUNT(*) FROM playlist_songs ps
		JOIN playlist_playlists p ON p.id = ps.playlist_id
		WHERE p.user_id = ? AND p.is_system_liked = 1 AND ps.song_id = ?`,
		uid.String(), sid.String()).Scan(&count)
	if count != 1 {
		t.Errorf("expected song in system-liked, got count=%d", count)
	}
}

func TestOnLiked_Idempotent(t *testing.T) {
	svc, d := newService(t)
	uid := seedUser(t, d)
	sid := seedSong(t, d)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := svc.OnLiked(ctx, bus.LikedSong{UserID: uid, SongID: sid}); err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
	}
	var count int
	_ = d.QueryRow(`SELECT COUNT(*) FROM playlist_songs WHERE song_id = ?`, sid.String()).Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 playlist_songs row, got %d", count)
	}
}

func TestOnUnliked_RemovesFromSystemLiked(t *testing.T) {
	svc, d := newService(t)
	uid := seedUser(t, d)
	sid := seedSong(t, d)
	ctx := context.Background()

	// Like then unlike.
	if err := svc.OnLiked(ctx, bus.LikedSong{UserID: uid, SongID: sid}); err != nil {
		t.Fatalf("like: %v", err)
	}
	if err := svc.OnUnliked(ctx, bus.UnlikedSong{UserID: uid, SongID: sid}); err != nil {
		t.Fatalf("unlike: %v", err)
	}

	var count int
	_ = d.QueryRow(`SELECT COUNT(*) FROM playlist_songs WHERE song_id = ?`, sid.String()).Scan(&count)
	if count != 0 {
		t.Errorf("expected 0 rows after unlike, got %d", count)
	}
}

func TestOnUnliked_Idempotent(t *testing.T) {
	svc, d := newService(t)
	uid := seedUser(t, d)
	sid := seedSong(t, d)
	ctx := context.Background()

	// Unlike without ever liking — should be no-op, not an error.
	if err := svc.OnUnliked(ctx, bus.UnlikedSong{UserID: uid, SongID: sid}); err != nil {
		t.Fatalf("unlike on empty: %v", err)
	}
}

// TestCreate_ListGet covers the HTTP-facing path for regular playlists.
func TestCreate_ListGet(t *testing.T) {
	svc, d := newService(t)
	uid := seedUser(t, d)
	ctx := context.Background()

	p, err := svc.Create(ctx, uid, playlist.CreatePlaylistRequest{
		Name:        "Deep Cuts",
		Description: "late-night",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.Name != "Deep Cuts" {
		t.Errorf("name mismatch: %q", p.Name)
	}

	list, err := svc.ListForUser(ctx, uid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// Expect at least the created playlist + the auto-created system-liked.
	if len(list) < 2 {
		t.Errorf("expected >=2 playlists, got %d", len(list))
	}

	got, err := svc.Get(ctx, uid, p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Playlist.ID != p.ID {
		t.Errorf("get returned wrong playlist")
	}
}

func TestCreate_EmptyNameRejected(t *testing.T) {
	svc, d := newService(t)
	uid := seedUser(t, d)
	ctx := context.Background()

	_, err := svc.Create(ctx, uid, playlist.CreatePlaylistRequest{Name: "  "})
	if err != playlist.ErrInvalidName {
		t.Errorf("got %v, want ErrInvalidName", err)
	}
}

// TestGet_OtherUser: Alice can't fetch Bob's playlist.
// Cross-user access returns ErrNotFound (→ 404), not ErrForbidden (→ 403),
// to prevent playlist-ID enumeration.
func TestGet_OtherUser(t *testing.T) {
	svc, d := newService(t)
	alice := seedUser(t, d)
	bob := seedUser(t, d)
	ctx := context.Background()

	pl, err := svc.Create(ctx, bob, playlist.CreatePlaylistRequest{Name: "Bob's mix"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = svc.Get(ctx, alice, pl.ID)
	if err != playlist.ErrNotFound {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

// TestDelete_OtherUser: cross-user delete returns ErrNotFound, not ErrForbidden.
func TestDelete_OtherUser(t *testing.T) {
	svc, d := newService(t)
	alice := seedUser(t, d)
	bob := seedUser(t, d)
	ctx := context.Background()

	pl, err := svc.Create(ctx, bob, playlist.CreatePlaylistRequest{Name: "Bob's mix"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Delete(ctx, alice, pl.ID); err != playlist.ErrNotFound {
		t.Errorf("got %v, want ErrNotFound", err)
	}
	// Bob's playlist must survive.
	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM playlist_playlists WHERE id = ?`, pl.ID.String()).Scan(&n)
	if n != 1 {
		t.Errorf("expected Bob's playlist to survive, got count=%d", n)
	}
}

// TestAddSong_OtherUser: cross-user AddSong returns ErrNotFound.
func TestAddSong_OtherUser(t *testing.T) {
	svc, d := newService(t)
	alice := seedUser(t, d)
	bob := seedUser(t, d)
	sid := seedSong(t, d)
	ctx := context.Background()

	pl, err := svc.Create(ctx, bob, playlist.CreatePlaylistRequest{Name: "Bob's mix"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.AddSong(ctx, alice, pl.ID, sid); err != playlist.ErrNotFound {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

// TestRemoveSong_OtherUser: cross-user RemoveSong returns ErrNotFound.
func TestRemoveSong_OtherUser(t *testing.T) {
	svc, d := newService(t)
	alice := seedUser(t, d)
	bob := seedUser(t, d)
	sid := seedSong(t, d)
	ctx := context.Background()

	pl, err := svc.Create(ctx, bob, playlist.CreatePlaylistRequest{Name: "Bob's mix"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.AddSong(ctx, bob, pl.ID, sid); err != nil {
		t.Fatalf("seed add: %v", err)
	}
	if err := svc.RemoveSong(ctx, alice, pl.ID, sid); err != playlist.ErrNotFound {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

// TestDelete_SystemLikedRefused: user cannot delete the system-liked playlist.
func TestDelete_SystemLikedRefused(t *testing.T) {
	svc, d := newService(t)
	uid := seedUser(t, d)
	ctx := context.Background()

	// Trigger system-liked creation.
	if err := svc.OnUserCreated(ctx, bus.UserCreated{UserID: uid}); err != nil {
		t.Fatalf("seed liked: %v", err)
	}
	list, _ := svc.ListForUser(ctx, uid)
	var likedID uuid.UUID
	for _, p := range list {
		// Can't tell is_system_liked from the response; just pick the "Liked" one.
		if p.Name == "Liked" {
			likedID = p.ID
			break
		}
	}
	if likedID == uuid.Nil {
		t.Fatal("no Liked playlist found")
	}
	err := svc.Delete(ctx, uid, likedID)
	if err != playlist.ErrForbidden {
		t.Errorf("got %v, want ErrForbidden", err)
	}
}

// TestAddRemoveSong covers the happy path for song manipulation via HTTP-facing methods.
func TestAddRemoveSong(t *testing.T) {
	svc, d := newService(t)
	uid := seedUser(t, d)
	sid := seedSong(t, d)
	ctx := context.Background()

	pl, err := svc.Create(ctx, uid, playlist.CreatePlaylistRequest{Name: "Mix"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := svc.AddSong(ctx, uid, pl.ID, sid); err != nil {
		t.Fatalf("add: %v", err)
	}
	got, _ := svc.Get(ctx, uid, pl.ID)
	if len(got.Songs) != 1 {
		t.Errorf("expected 1 song, got %d", len(got.Songs))
	}

	if err := svc.RemoveSong(ctx, uid, pl.ID, sid); err != nil {
		t.Fatalf("remove: %v", err)
	}
	got, _ = svc.Get(ctx, uid, pl.ID)
	if len(got.Songs) != 0 {
		t.Errorf("expected 0 songs, got %d", len(got.Songs))
	}
}
