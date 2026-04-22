package similarity

import (
	"context"
	"errors"
	"math/rand"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// --- test fixtures ---

// fakeBucket is a Bucket that returns a canned candidate list with
// configurable id, label, weight, and emit function. Lets us
// exercise the engine merge math without spinning up Discogs.
type fakeBucket struct {
	id     string
	label  string
	weight float64
	emit   func(seed Seed) []Candidate
}

func (b *fakeBucket) ID() string             { return b.id }
func (b *fakeBucket) Label() string          { return b.label }
func (b *fakeBucket) Description() string    { return "" }
func (b *fakeBucket) DefaultWeight() float64 { return b.weight }
func (b *fakeBucket) Candidates(_ context.Context, seed Seed) ([]Candidate, error) {
	if b.emit == nil {
		return nil, nil
	}
	return b.emit(seed), nil
}

// erroringBucket asserts the engine swallows bucket errors.
type erroringBucket struct{ id string }

func (b *erroringBucket) ID() string             { return b.id }
func (b *erroringBucket) Label() string          { return b.id }
func (b *erroringBucket) Description() string    { return "" }
func (b *erroringBucket) DefaultWeight() float64 { return 1 }
func (b *erroringBucket) Candidates(_ context.Context, _ Seed) ([]Candidate, error) {
	return nil, errors.New("buckets gonna bucket")
}

// stubSeedReader returns a fixed Seed for any (userID, songID).
type stubSeedReader struct{ seed Seed }

func (r *stubSeedReader) ReadSeed(_ context.Context, userID, songID uuid.UUID) (Seed, error) {
	out := r.seed
	out.UserID = userID
	out.SongID = songID
	return out, nil
}

// stubAcquirer records calls; SongID returned is a fresh UUID so
// callers see the contract honored.
type stubAcquirer struct {
	calls []struct{ Title, Artist, ImageURL string }
}

func (a *stubAcquirer) AcquireForUser(_ context.Context, _ uuid.UUID, title, artist, image string) (uuid.UUID, error) {
	a.calls = append(a.calls, struct{ Title, Artist, ImageURL string }{title, artist, image})
	return uuid.New(), nil
}

// stubDeduper says HasEntry true for any (artist, title) in dupes.
type stubDeduper struct{ dupes map[string]bool }

func (d *stubDeduper) HasEntry(_ context.Context, _ uuid.UUID, title, artist string) bool {
	if d.dupes == nil {
		return false
	}
	return d.dupes[strings.ToLower(artist)+"\x00"+strings.ToLower(title)]
}

// --- engine tests ---

// TestEngine_EmptyRegistry: zero buckets is a clean empty state,
// not an error. Important so PR A ships before any buckets exist.
func TestEngine_EmptyRegistry(t *testing.T) {
	e := newEngine(nil, NewNoopWeightStore(), nil, nil)
	_, ok := e.pick(context.Background(), Seed{UserID: uuid.New(), SongID: uuid.New(),
		Title: "X", Artist: "Y"})
	if ok {
		t.Errorf("empty registry must return ok=false")
	}
}

// TestEngine_MergeAcrossBuckets: a candidate appearing in two
// buckets accumulates score = sum(weight × confidence). Verifies
// the core ranking primitive — without this every other behavior
// is moot.
func TestEngine_MergeAcrossBuckets(t *testing.T) {
	b1 := &fakeBucket{id: "b1", weight: 5, emit: func(_ Seed) []Candidate {
		return []Candidate{{Title: "Discovery", Artist: "Daft Punk", Confidence: 1}}
	}}
	b2 := &fakeBucket{id: "b2", weight: 3, emit: func(_ Seed) []Candidate {
		return []Candidate{{Title: "Discovery", Artist: "Daft Punk", Confidence: 1}}
	}}

	// Pin the RNG so the single-candidate weighted pick is deterministic.
	rng := rand.New(rand.NewSource(42)) //nolint:gosec
	e := newEngine([]Bucket{b1, b2}, NewNoopWeightStore(), nil, rng)
	picked, ok := e.pick(context.Background(), seed("X", "Y"))
	if !ok {
		t.Fatal("expected a pick")
	}
	if picked.Score != 8 {
		t.Errorf("score = %v, want 8 (5+3)", picked.Score)
	}
	if len(picked.Buckets) != 2 {
		t.Errorf("contributing buckets = %v, want 2", picked.Buckets)
	}
}

// TestEngine_DropsSeedSelf: a bucket like "same artist" naturally
// returns the seed itself; engine must filter it. Without this,
// turning on similar mode for "Discovery" would queue Discovery again.
func TestEngine_DropsSeedSelf(t *testing.T) {
	b1 := &fakeBucket{id: "b1", weight: 5, emit: func(_ Seed) []Candidate {
		return []Candidate{
			{Title: "discovery", Artist: "DAFT PUNK", Confidence: 1}, // case drift
			{Title: "Homework", Artist: "Daft Punk", Confidence: 1},
		}
	}}
	rng := rand.New(rand.NewSource(1)) //nolint:gosec
	e := newEngine([]Bucket{b1}, NewNoopWeightStore(), nil, rng)
	picked, ok := e.pick(context.Background(), seed("Discovery", "Daft Punk"))
	if !ok {
		t.Fatal("expected a pick")
	}
	if strings.EqualFold(picked.Title, "Discovery") {
		t.Errorf("seed self leaked through dedup: %+v", picked)
	}
}

// TestEngine_DedupeAgainstQueue: candidates already in the user's
// queue get dropped. With only-dupe input, engine returns ok=false.
func TestEngine_DedupeAgainstQueue(t *testing.T) {
	b1 := &fakeBucket{id: "b1", weight: 5, emit: func(_ Seed) []Candidate {
		return []Candidate{{Title: "Homework", Artist: "Daft Punk", Confidence: 1}}
	}}
	dedup := &stubDeduper{dupes: map[string]bool{
		"daft punk\x00homework": true,
	}}
	e := newEngine([]Bucket{b1}, NewNoopWeightStore(), dedup, rand.New(rand.NewSource(1))) //nolint:gosec
	if _, ok := e.pick(context.Background(), seed("X", "Y")); ok {
		t.Errorf("expected ok=false when every candidate is a dedup hit")
	}
}

// TestEngine_BucketErrorIsSwallowed: one buggy bucket shouldn't
// stall the cycle. Healthy buckets contribute as normal.
func TestEngine_BucketErrorIsSwallowed(t *testing.T) {
	good := &fakeBucket{id: "good", weight: 5, emit: func(_ Seed) []Candidate {
		return []Candidate{{Title: "Discovery", Artist: "Daft Punk", Confidence: 1}}
	}}
	bad := &erroringBucket{id: "bad"}
	rng := rand.New(rand.NewSource(7)) //nolint:gosec
	e := newEngine([]Bucket{good, bad}, NewNoopWeightStore(), nil, rng)
	picked, ok := e.pick(context.Background(), seed("X", "Y"))
	if !ok {
		t.Fatal("expected a pick despite the erroring bucket")
	}
	if picked.Score != 5 {
		t.Errorf("score = %v, want 5 (only the good bucket contributed)", picked.Score)
	}
}

// TestEngine_UserWeightOverridesDefault: WeightStore-supplied
// weight wins; explicit 0 disables the bucket entirely.
func TestEngine_UserWeightOverridesDefault(t *testing.T) {
	loud := &fakeBucket{id: "loud", weight: 5, emit: func(_ Seed) []Candidate {
		return []Candidate{{Title: "A", Artist: "X", Confidence: 1}}
	}}
	muted := &fakeBucket{id: "muted", weight: 5, emit: func(_ Seed) []Candidate {
		return []Candidate{{Title: "B", Artist: "Y", Confidence: 1}}
	}}
	weights := stubWeights{"muted": 0}
	rng := rand.New(rand.NewSource(13)) //nolint:gosec
	e := newEngine([]Bucket{loud, muted}, weights, nil, rng)
	picked, ok := e.pick(context.Background(), seed("seed", "seed"))
	if !ok {
		t.Fatal("expected a pick")
	}
	if picked.Title != "A" {
		t.Errorf("got %s, want A (B was disabled via weight=0)", picked.Title)
	}
}

// stubWeights is an inline WeightStore fixture.
type stubWeights map[string]float64

func (w stubWeights) WeightsFor(_ context.Context, _ uuid.UUID) (map[string]float64, error) {
	return map[string]float64(w), nil
}

// --- Service tests ---

// TestService_EmptyRegistry_NextPickReturnsNoCandidates is the
// PR A acceptance check: a Service with no Register calls returns
// ErrNoCandidates, not a panic, not a nil-deref. Refiller treats
// this as "fall back to genre-random."
func TestService_EmptyRegistry_NextPickReturnsNoCandidates(t *testing.T) {
	s := NewService(Config{
		SeedReader:   &stubSeedReader{seed: Seed{Title: "X", Artist: "Y"}},
		SongAcquirer: &stubAcquirer{},
	})
	_, err := s.NextPick(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, ErrNoCandidates) {
		t.Errorf("got %v, want ErrNoCandidates", err)
	}
}

// TestService_SeedWithNoMetadata returns ErrSeedUnknown — the
// frontend uses this to flip the lens icon to its degraded state.
func TestService_SeedWithNoMetadata(t *testing.T) {
	s := NewService(Config{
		SeedReader:   &stubSeedReader{seed: Seed{}}, // empty title + artist
		SongAcquirer: &stubAcquirer{},
	})
	s.Register(&fakeBucket{id: "b1", weight: 5, emit: func(_ Seed) []Candidate {
		return []Candidate{{Title: "X", Artist: "Y"}}
	}})
	_, err := s.NextPick(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, ErrSeedUnknown) {
		t.Errorf("got %v, want ErrSeedUnknown", err)
	}
}

// TestService_RegisterAndBuckets verifies the registry snapshot
// API the v0.5 PR D settings UI will read from.
func TestService_RegisterAndBuckets(t *testing.T) {
	s := NewService(Config{
		SeedReader:   &stubSeedReader{seed: Seed{Title: "X", Artist: "Y"}},
		SongAcquirer: &stubAcquirer{},
	})
	if got := len(s.Buckets()); got != 0 {
		t.Errorf("fresh registry len = %d, want 0", got)
	}
	s.Register(&fakeBucket{id: "a", weight: 1})
	s.Register(&fakeBucket{id: "b", weight: 2})
	got := s.Buckets()
	if len(got) != 2 || got[0].ID() != "a" || got[1].ID() != "b" {
		t.Errorf("registry order broken: %+v", ids(got))
	}
}

func ids(bs []Bucket) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.ID()
	}
	return out
}

// seed is a tiny ctor for fixture seeds.
func seed(title, artist string) Seed {
	return Seed{
		SongID: uuid.New(),
		UserID: uuid.New(),
		Title:  title,
		Artist: artist,
	}
}
