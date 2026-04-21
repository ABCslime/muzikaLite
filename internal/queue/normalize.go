package queue

import (
	"strings"
	"unicode"
)

// normalizeQuery applies the v0.4 PR 3 user-search normalization per
// ROADMAP §v0.4 item 5:
//
//   - lowercase
//   - strip punctuation (any rune that is not a letter, digit, or space)
//   - collapse whitespace to a single space
//
// Returns the normalized query. Empty input returns empty output; callers
// are expected to fall back to retryLongWords in that case. The function
// is intentionally dumb: no fuzzy matching, no stemming, no locale rules.
// Discogs' own search does the fuzzy work per ROADMAP — "no custom fuzzy
// matcher."
//
// Note: we keep Unicode letters/digits beyond ASCII (so "Björk" stays
// "björk" rather than becoming "bj rk"). That matches what users typing
// artist names actually type and what Discogs indexes.
func normalizeQuery(q string) string {
	if q == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(q))
	prevSpace := true // suppress leading spaces
	for _, r := range q {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
			prevSpace = false
		case unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r):
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
		default:
			// Controls and other weirdness — drop.
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// retryLongWords is the fallback when normalizeQuery produces empty
// output or the primary Discogs call returns nothing: keep only words
// of more than 4 characters, hoping that stripping short/common words
// ("the", "in", "an") helps Discogs' indexer find a match. ROADMAP:
// "If empty, retry with words > 4 chars only."
//
// Applied to the original (post-normalize) query string. If nothing
// survives, returns "" — the caller treats that as a genuine no-match.
func retryLongWords(normalized string) string {
	if normalized == "" {
		return ""
	}
	fields := strings.Fields(normalized)
	out := fields[:0]
	for _, w := range fields {
		if len([]rune(w)) > 4 {
			out = append(out, w)
		}
	}
	return strings.Join(out, " ")
}
