package preferences

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/google/uuid"
)

// maxItemsPerSource caps how many tags/genres a user can pin per source.
// Arbitrary — big enough to never hit in normal use, small enough that a
// malicious or buggy client can't blow up the DB.
const maxItemsPerSource = 50

// maxItemLength caps the length of a single tag/genre string.
const maxItemLength = 64

// ErrTooMany is returned by Replace when a source's list exceeds maxItemsPerSource.
var ErrTooMany = errors.New("preferences: too many items for one source")

// ErrItemTooLong is returned when a single tag/genre exceeds maxItemLength.
var ErrItemTooLong = errors.New("preferences: item string too long")

// Service wraps Repo with validation + deduplication.
type Service struct {
	repo *Repo
	log  *slog.Logger
}

// NewService constructs a Service over the given Repo.
func NewService(r *Repo) *Service {
	return &Service{repo: r, log: slog.Default().With("mod", "preferences")}
}

// Get returns userID's preferences. Nil slices normalize to empty slices
// so the JSON response is `{"bandcampTags": [], "discogsGenres": []}`
// rather than `{"bandcampTags": null, ...}` — easier for the frontend.
func (s *Service) Get(ctx context.Context, userID uuid.UUID) (Preferences, error) {
	p, err := s.repo.Get(ctx, userID)
	if err != nil {
		return Preferences{}, err
	}
	if p.BandcampTags == nil {
		p.BandcampTags = []string{}
	}
	if p.DiscogsGenres == nil {
		p.DiscogsGenres = []string{}
	}
	return p, nil
}

// Replace validates + normalizes + persists p for userID.
//
// Normalization:
//   - trim whitespace
//   - drop empty entries
//   - deduplicate case-insensitively, keeping the first occurrence's casing
//
// Validation:
//   - max maxItemsPerSource items per source
//   - max maxItemLength characters per item
//
// Case handling: we preserve the user's input casing rather than force
// lowercase because Discogs' canonical genres are capitalized ("Electronic",
// "Hip Hop") and the Discogs API treats them case-sensitively. Bandcamp
// tags are usually lowercase-hyphenated; we don't force that either.
func (s *Service) Replace(ctx context.Context, userID uuid.UUID, p Preferences) error {
	tags, err := normalize(p.BandcampTags)
	if err != nil {
		return err
	}
	genres, err := normalize(p.DiscogsGenres)
	if err != nil {
		return err
	}
	return s.repo.Replace(ctx, userID, Preferences{
		BandcampTags:  tags,
		DiscogsGenres: genres,
	})
}

func normalize(items []string) ([]string, error) {
	if len(items) > maxItemsPerSource*2 {
		// Early fast-fail before dedup math.
		return nil, ErrTooMany
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, raw := range items {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		if len([]rune(v)) > maxItemLength {
			return nil, ErrItemTooLong
		}
		key := strings.ToLower(v)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, v)
	}
	if len(out) > maxItemsPerSource {
		return nil, ErrTooMany
	}
	return out, nil
}
