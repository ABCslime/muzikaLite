// Package filematch provides fuzzy matchers for comparing Discogs
// metadata (release titles, artist names) against Soulseek filenames.
//
// The central operation is: does a Soulseek filename (which often
// contains a full file path with artist/album/track slots) represent
// a given release title? Soulseek users don't standardize filenames
// — they preserve punctuation, parentheticals, unicode accents,
// and album-specific cruft. A literal string match is brittle.
//
// filematch normalizes both sides to a whitespace-separated,
// lowercased, punctuation-stripped, parenthetical-stripped form and
// compares as a token set: every token of the search side must
// appear as a word in the filename side. This is robust to:
//
//   - Case differences (lower = lower)
//   - Punctuation variance ("A & B" vs "A and B" vs "A, B"):
//     punctuation stripped on both sides before matching
//   - Parentheticals ("Discovery (Remastered)" vs "Discovery"):
//     paren expressions removed before tokenizing
//   - Path separators (backslashes in Soulseek paths): treated as
//     whitespace
//   - Stopwords ("The Wall" vs "Wall"): English articles/prepositions
//     dropped from the title side before matching; filenames can
//     contain them or not
//
// It does NOT help with:
//
//   - Accent variance ("Björk" vs "Bjork"): we leave unicode letters
//     alone rather than lossy-ASCII-fold, because some users DO
//     include accents and dropping them en masse loses more than it
//     gains. Callers that want accent-insensitive match should add
//     an explicit unicode-folding pass.
//   - Word-order semantics: we match as a set, not a sequence. "Me
//     Go Never Let" matches "Never Let Me Go". In practice filename
//     word-order usually follows the canonical, so this is fine.
//
// v0.4.2 PR E.
package filematch

import (
	"regexp"
	"strings"
	"unicode"
)

// parenRE matches () or [] and their contents. Greedy within one
// pair; nested parens get reduced piece by piece by running the
// regex to fixed point — one pass is enough for common cases.
var parenRE = regexp.MustCompile(`[\(\[][^\)\]]*[\)\]]`)

// stopwords are English articles/prepositions dropped from the title
// side before token matching. Filenames in practice omit most of
// these; requiring them would create false negatives on common
// releases like "The Wall" or "A Hard Day's Night".
var stopwords = map[string]struct{}{
	"the": {}, "a": {}, "an": {}, "of": {}, "in": {}, "on": {},
	"at": {}, "to": {}, "for": {}, "from": {}, "by": {},
}

// Normalize returns a matchable form of s:
//   - parenthetical expressions removed
//   - every non-alphanumeric run replaced with a single space
//   - ASCII case folded (IsLetter keeps unicode letters intact)
//   - leading/trailing whitespace trimmed
//
// Example: "Florence + The Machine (Deluxe)" → "florence the machine"
// Example: "@@user\\Music\\Artist\\01 - Track.flac" → "user music artist 01 track flac"
func Normalize(s string) string {
	s = parenRE.ReplaceAllString(s, " ")
	var b strings.Builder
	b.Grow(len(s))
	atSpace := true
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
			atSpace = false
		default:
			if !atSpace {
				b.WriteByte(' ')
				atSpace = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// Tokens returns the significant words of s for matching purposes:
// Normalize(s) split on whitespace, stopwords dropped, duplicates kept
// (order matters for iteration ergonomics but not semantics).
//
// Empty input or input that reduces to only stopwords returns an
// empty slice — callers should handle that as "nothing to match on".
func Tokens(s string) []string {
	raw := strings.Fields(Normalize(s))
	out := raw[:0]
	for _, t := range raw {
		if _, skip := stopwords[t]; skip {
			continue
		}
		out = append(out, t)
	}
	return out
}

// Contains reports whether every token in `tokens` appears as a word
// in Normalize(filename). Empty tokens returns true (matches-any).
//
// Membership is exact, not substring — "aa" in filename tokens
// {"kawasaki", "song"} is false (no bare "aa"). This avoids the
// short-token false-positive trap that plain substring matching
// runs into on tracks like Merzbow's "Aa".
func Contains(filename string, tokens []string) bool {
	if len(tokens) == 0 {
		return true
	}
	fileSet := make(map[string]struct{}, 16)
	for _, t := range strings.Fields(Normalize(filename)) {
		fileSet[t] = struct{}{}
	}
	for _, t := range tokens {
		if _, ok := fileSet[t]; !ok {
			return false
		}
	}
	return true
}

// MatchesTitle is the 95%-case shorthand: does filename contain all
// tokens of title? Convenience over Tokens + Contains so callers
// don't re-tokenize title for every filename they check.
func MatchesTitle(filename, title string) bool {
	return Contains(filename, Tokens(title))
}
