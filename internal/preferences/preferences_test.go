package preferences_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/db"
	"github.com/macabc/muzika/internal/preferences"
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

// TestGetEmpty: a brand-new user with no prefs gets empty slices, not an error.
func TestGetEmpty(t *testing.T) {
	d := setupDB(t)
	uid := seedUser(t, d)
	svc := preferences.NewService(preferences.NewRepo(d))

	p, err := svc.Get(context.Background(), uid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(p.BandcampTags) != 0 || len(p.DiscogsGenres) != 0 {
		t.Errorf("expected empty, got %+v", p)
	}
}

// TestReplaceAndGet: write some prefs, read them back.
func TestReplaceAndGet(t *testing.T) {
	d := setupDB(t)
	uid := seedUser(t, d)
	svc := preferences.NewService(preferences.NewRepo(d))
	ctx := context.Background()

	in := preferences.Preferences{
		BandcampTags:  []string{"progressive-house", "vaporwave"},
		DiscogsGenres: []string{"Electronic", "Jazz"},
	}
	if err := svc.Replace(ctx, uid, in); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	out, err := svc.Get(ctx, uid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !sameSet(out.BandcampTags, in.BandcampTags) {
		t.Errorf("bandcamp mismatch: got %v, want %v", out.BandcampTags, in.BandcampTags)
	}
	if !sameSet(out.DiscogsGenres, in.DiscogsGenres) {
		t.Errorf("discogs mismatch: got %v, want %v", out.DiscogsGenres, in.DiscogsGenres)
	}
}

// TestReplaceNormalizes: whitespace, empty items, and case-insensitive
// duplicates are cleaned up before persistence.
func TestReplaceNormalizes(t *testing.T) {
	d := setupDB(t)
	uid := seedUser(t, d)
	svc := preferences.NewService(preferences.NewRepo(d))
	ctx := context.Background()

	in := preferences.Preferences{
		BandcampTags:  []string{"  house  ", "", "HOUSE", "techno"},
		DiscogsGenres: []string{"Electronic", "ELECTRONIC", "  "},
	}
	if err := svc.Replace(ctx, uid, in); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	out, _ := svc.Get(ctx, uid)

	// Bandcamp: "house" (first case-insensitive occurrence) + "techno".
	if len(out.BandcampTags) != 2 {
		t.Errorf("bandcamp dedupe failed: %v", out.BandcampTags)
	}
	// Discogs: "Electronic" only.
	if len(out.DiscogsGenres) != 1 || out.DiscogsGenres[0] != "Electronic" {
		t.Errorf("discogs dedupe failed: %v", out.DiscogsGenres)
	}
}

// TestReplaceOverwrites: second Replace replaces, not appends.
func TestReplaceOverwrites(t *testing.T) {
	d := setupDB(t)
	uid := seedUser(t, d)
	svc := preferences.NewService(preferences.NewRepo(d))
	ctx := context.Background()

	_ = svc.Replace(ctx, uid, preferences.Preferences{
		BandcampTags: []string{"a", "b"},
	})
	_ = svc.Replace(ctx, uid, preferences.Preferences{
		BandcampTags: []string{"c"},
	})
	out, _ := svc.Get(ctx, uid)
	if len(out.BandcampTags) != 1 || out.BandcampTags[0] != "c" {
		t.Errorf("replace should overwrite, got %v", out.BandcampTags)
	}
}

// TestReplaceTooMany: more than maxItemsPerSource entries returns ErrTooMany.
func TestReplaceTooMany(t *testing.T) {
	d := setupDB(t)
	uid := seedUser(t, d)
	svc := preferences.NewService(preferences.NewRepo(d))
	ctx := context.Background()

	tags := make([]string, 60)
	for i := range tags {
		tags[i] = "tag" + string(rune('a'+i%26)) + "-" + string(rune('0'+i/26))
	}
	err := svc.Replace(ctx, uid, preferences.Preferences{BandcampTags: tags})
	if err != preferences.ErrTooMany {
		t.Errorf("got %v, want ErrTooMany", err)
	}
}

// TestReplaceItemTooLong: strings over 64 chars are rejected.
func TestReplaceItemTooLong(t *testing.T) {
	d := setupDB(t)
	uid := seedUser(t, d)
	svc := preferences.NewService(preferences.NewRepo(d))
	ctx := context.Background()

	tooLong := make([]byte, 65)
	for i := range tooLong {
		tooLong[i] = 'a'
	}
	err := svc.Replace(ctx, uid, preferences.Preferences{
		BandcampTags: []string{string(tooLong)},
	})
	if err != preferences.ErrItemTooLong {
		t.Errorf("got %v, want ErrItemTooLong", err)
	}
}

// TestCascadeOnUserDelete: deleting the user removes their preference rows.
func TestCascadeOnUserDelete(t *testing.T) {
	d := setupDB(t)
	uid := seedUser(t, d)
	svc := preferences.NewService(preferences.NewRepo(d))
	ctx := context.Background()

	_ = svc.Replace(ctx, uid, preferences.Preferences{
		BandcampTags:  []string{"a", "b"},
		DiscogsGenres: []string{"Electronic"},
	})
	if _, err := d.Exec(`DELETE FROM auth_users WHERE id = ?`, uid.String()); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	var bN, dN int
	_ = d.QueryRow(`SELECT COUNT(*) FROM user_bandcamp_tags WHERE user_id = ?`, uid.String()).Scan(&bN)
	_ = d.QueryRow(`SELECT COUNT(*) FROM user_discogs_genres WHERE user_id = ?`, uid.String()).Scan(&dN)
	if bN != 0 || dN != 0 {
		t.Errorf("cascade failed: bandcamp=%d, discogs=%d", bN, dN)
	}
}

// sameSet reports whether a and b contain the same elements (order-insensitive).
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, v := range a {
		seen[v] = true
	}
	for _, v := range b {
		if !seen[v] {
			return false
		}
	}
	return true
}
