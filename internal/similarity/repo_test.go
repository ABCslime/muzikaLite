package similarity_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/db"
	"github.com/macabc/muzika/internal/similarity"
)

// setupDB wires an on-disk SQLite with every migration applied.
// Mirrors the queue package's pattern (internal/queue/queue_test.go)
// so test shapes stay consistent across modules.
func setupDB(t *testing.T) *sql.DB {
	t.Helper()
	tmp := t.TempDir()
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatalf("mkdir tmp: %v", err)
	}
	d, err := db.Open(filepath.Join(tmp, "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := db.MigrateEmbedded(d); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return d
}

// seedUser + seedSong seed the referenced-by rows the migrations
// require (FK cascade is the whole point of the test that follows).
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

func seedSong(t *testing.T, d *sql.DB, requesterID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := d.Exec(
		`INSERT INTO queue_songs (id, title, artist, requesting_user_id)
		 VALUES (?, ?, ?, ?)`,
		id.String(), "Test Title", "Test Artist", requesterID.String(),
	); err != nil {
		t.Fatalf("seed song: %v", err)
	}
	return id
}

// TestRepo_SeedRoundTrip verifies the basic upsert behavior: Set
// then Get returns what was written; clearing reverts to
// uuid.Nil.
func TestRepo_SeedRoundTrip(t *testing.T) {
	d := setupDB(t)
	repo := similarity.NewRepo(d)
	ctx := context.Background()
	user := seedUser(t, d)
	song := seedSong(t, d, user)

	got, err := repo.SeedFor(ctx, user)
	if err != nil || got != uuid.Nil {
		t.Fatalf("fresh user: got (%v, %v), want (uuid.Nil, nil)", got, err)
	}
	if err := repo.SetSeed(ctx, user, song); err != nil {
		t.Fatalf("SetSeed: %v", err)
	}
	got, err = repo.SeedFor(ctx, user)
	if err != nil || got != song {
		t.Errorf("after SetSeed: got (%v, %v), want (%v, nil)", got, err, song)
	}
	if err := repo.SetSeed(ctx, user, uuid.Nil); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	got, err = repo.SeedFor(ctx, user)
	if err != nil || got != uuid.Nil {
		t.Errorf("after clear: got (%v, %v), want (uuid.Nil, nil)", got, err)
	}
}

// TestRepo_CascadeClearsSeedOnSongDelete is the v0.5 PR B
// migration 0008 contract in concrete form: when the queue_songs
// row referenced by seed_song_id is deleted, the FK's ON DELETE
// SET NULL fires and the user's seed clears automatically. The
// frontend's next hydrate() call sees Active=false and the lens
// icon flips off — no background sweeper required.
//
// Worth a dedicated test: the SET NULL behavior is what lets us
// avoid tracking "seed was removed from queue" as its own event.
// If a future migration changes the FK clause (RESTRICT, CASCADE
// on the parent), the lens icon UX breaks silently without this.
func TestRepo_CascadeClearsSeedOnSongDelete(t *testing.T) {
	d := setupDB(t)
	repo := similarity.NewRepo(d)
	ctx := context.Background()
	user := seedUser(t, d)
	song := seedSong(t, d, user)

	if err := repo.SetSeed(ctx, user, song); err != nil {
		t.Fatalf("SetSeed: %v", err)
	}
	if got, _ := repo.SeedFor(ctx, user); got != song {
		t.Fatalf("precondition: expected seed=%v, got %v", song, got)
	}

	// The FK on user_similarity_settings.seed_song_id references
	// queue_songs(id) with ON DELETE SET NULL. Deleting the song
	// should leave the user_similarity_settings row intact but
	// clear the seed_song_id column.
	if _, err := d.ExecContext(ctx,
		`DELETE FROM queue_songs WHERE id = ?`, song.String()); err != nil {
		t.Fatalf("delete queue_songs row: %v", err)
	}
	got, err := repo.SeedFor(ctx, user)
	if err != nil {
		t.Fatalf("SeedFor: %v", err)
	}
	if got != uuid.Nil {
		t.Errorf("cascade SET NULL didn't fire: seed still = %v", got)
	}

	// The user_similarity_settings row should still exist (it
	// just has seed_song_id = NULL now). Otherwise a
	// subsequent SetSeed would have to re-insert rather than
	// upsert — not a correctness issue today because SetSeed
	// uses ON CONFLICT, but worth nailing down.
	var count int
	if err := d.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM user_similarity_settings WHERE user_id = ?`,
		user.String(),
	).Scan(&count); err != nil {
		t.Fatalf("count row: %v", err)
	}
	if count != 1 {
		t.Errorf("expected row to survive cascade, got %d rows", count)
	}
}

// TestRepo_WeightsRoundTrip exercises PR D's bucket_weights JSON
// path: empty → set → read → clear. Negative values clamp to 0
// on write (the engine also clamps on read, but cleaning both
// places keeps storage honest).
func TestRepo_WeightsRoundTrip(t *testing.T) {
	d := setupDB(t)
	repo := similarity.NewRepo(d)
	ctx := context.Background()
	user := seedUser(t, d)

	got, err := repo.WeightsFor(ctx, user)
	if err != nil || got != nil {
		t.Fatalf("fresh user: got (%v, %v), want (nil, nil)", got, err)
	}

	// Set a sparse map including a negative — backend should clamp to 0.
	write := map[string]float64{
		"discogs.same_artist":    7.5,
		"discogs.same_label_era": 0,
		"discogs.collaborators":  -3, // must clamp to 0
	}
	if err := repo.SetWeights(ctx, user, write); err != nil {
		t.Fatalf("SetWeights: %v", err)
	}
	got, err = repo.WeightsFor(ctx, user)
	if err != nil {
		t.Fatalf("WeightsFor: %v", err)
	}
	if got["discogs.same_artist"] != 7.5 {
		t.Errorf("same_artist: got %v, want 7.5", got["discogs.same_artist"])
	}
	if got["discogs.collaborators"] != 0 {
		t.Errorf("negative didn't clamp on write: got %v, want 0", got["discogs.collaborators"])
	}

	// Clearing to nil / empty map reverts to "no row tuned,
	// engine falls through to defaults."
	if err := repo.SetWeights(ctx, user, nil); err != nil {
		t.Fatalf("clear SetWeights: %v", err)
	}
	got, err = repo.WeightsFor(ctx, user)
	if err != nil || got != nil {
		t.Errorf("after clear: got (%v, %v), want (nil, nil)", got, err)
	}
}
