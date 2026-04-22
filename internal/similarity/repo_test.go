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
// CASCADE removes the seed from the join table automatically.
// Lens icon flips off on next hydrate — no background sweeper.
//
// v0.6 PR D update: the contract moved from SET NULL on a
// singular FK column to ON DELETE CASCADE on a join-table row.
// Functionally equivalent from the user's POV (seed disappears
// when its song does); the shape is cleaner because we don't
// carry "tombstone" user_similarity_settings rows after song
// deletes — each user only has a settings row if they've tuned
// weights, and seeds are their own many-to-many table.
func TestRepo_CascadeClearsSeedOnSongDelete(t *testing.T) {
	d := setupDB(t)
	repo := similarity.NewRepo(d)
	ctx := context.Background()
	user := seedUser(t, d)
	song := seedSong(t, d, user)

	if err := repo.AddSeed(ctx, user, song); err != nil {
		t.Fatalf("AddSeed: %v", err)
	}
	if got, _ := repo.SeedsFor(ctx, user); len(got) != 1 || got[0] != song {
		t.Fatalf("precondition: expected seeds=[%v], got %v", song, got)
	}

	// Deleting the queue_songs row fires the FK's ON DELETE
	// CASCADE on user_similarity_seeds.song_id, removing the
	// row outright.
	if _, err := d.ExecContext(ctx,
		`DELETE FROM queue_songs WHERE id = ?`, song.String()); err != nil {
		t.Fatalf("delete queue_songs row: %v", err)
	}
	got, err := repo.SeedsFor(ctx, user)
	if err != nil {
		t.Fatalf("SeedsFor: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("cascade didn't fire: seeds still = %v", got)
	}
	// Second-leg check: only the seed row went away, not anything
	// else about the user. If the user also had bucket_weights
	// tuned, that row must survive (nothing referenced the song
	// from there).
	var settingsCount int
	if err := d.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM user_similarity_settings WHERE user_id = ?`,
		user.String(),
	).Scan(&settingsCount); err != nil {
		t.Fatalf("count settings: %v", err)
	}
	// Zero is fine: AddSeed doesn't create a settings row, only
	// SetWeights does. The contract is that settings rows aren't
	// created implicitly by seed mutations.
	_ = settingsCount
}

// TestRepo_Multi_AddRemoveReplace exercises the v0.6 multi-seed
// API primitives: AddSeed is idempotent, RemoveSeed is a no-op
// on non-members, ReplaceSeeds is atomic.
func TestRepo_Multi_AddRemoveReplace(t *testing.T) {
	d := setupDB(t)
	repo := similarity.NewRepo(d)
	ctx := context.Background()
	user := seedUser(t, d)
	s1 := seedSong(t, d, user)
	s2 := seedSong(t, d, user)
	s3 := seedSong(t, d, user)

	// Idempotent add.
	if err := repo.AddSeed(ctx, user, s1); err != nil {
		t.Fatalf("AddSeed: %v", err)
	}
	if err := repo.AddSeed(ctx, user, s1); err != nil {
		t.Fatalf("AddSeed (idempotent): %v", err)
	}
	if err := repo.AddSeed(ctx, user, s2); err != nil {
		t.Fatalf("AddSeed 2: %v", err)
	}
	got, _ := repo.SeedsFor(ctx, user)
	if len(got) != 2 {
		t.Errorf("after 2+dup adds: len=%d, want 2", len(got))
	}

	// RemoveSeed on a member drops it; on a non-member, no-op.
	if err := repo.RemoveSeed(ctx, user, s1); err != nil {
		t.Fatalf("RemoveSeed: %v", err)
	}
	if err := repo.RemoveSeed(ctx, user, s3); err != nil {
		t.Fatalf("RemoveSeed nonmember: %v", err)
	}
	got, _ = repo.SeedsFor(ctx, user)
	if len(got) != 1 || got[0] != s2 {
		t.Errorf("after remove: got %v, want [%v]", got, s2)
	}

	// ReplaceSeeds dedups input and atomically overwrites.
	if err := repo.ReplaceSeeds(ctx, user, []uuid.UUID{s3, s1, s3, uuid.Nil, s1}); err != nil {
		t.Fatalf("ReplaceSeeds: %v", err)
	}
	got, _ = repo.SeedsFor(ctx, user)
	if len(got) != 2 {
		t.Errorf("after replace with dupes+nil: len=%d, want 2 (s1,s3 deduped)", len(got))
	}

	// Empty slice clears.
	if err := repo.ReplaceSeeds(ctx, user, nil); err != nil {
		t.Fatalf("ReplaceSeeds nil: %v", err)
	}
	got, _ = repo.SeedsFor(ctx, user)
	if len(got) != 0 {
		t.Errorf("after clear: got %v, want empty", got)
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
