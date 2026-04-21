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

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// newAccentFold constructs a fresh NFD + drop-combining-marks
// transform chain — the canonical "Björk → Bjork" fold. Applied
// in Normalize so downstream token comparisons are accent-
// insensitive; Discogs titles often preserve accents (Privé,
// Björk, Ágætis) while Soulseek users routinely type the ASCII
// form. Without folding, the filter loses hits on real matches.
//
// x/text Transformers are stateful (they keep an internal buffer),
// so sharing a single Chain across goroutines panics on slice
// bounds when two searches race. We construct a new chain per
// Normalize call — the allocations are tiny (three no-op wrappers
// around global norm.Form / runes.Set values) and this is the
// simplest thread-safe pattern.
func newAccentFold() transform.Transformer {
	return transform.Chain(
		norm.NFD,
		runes.Remove(runes.In(unicode.Mn)),
		norm.NFC,
	)
}

// ligatureFold handles the Latin letters that NFD can't split because
// they're atomic codepoints, not composed ones. Discogs preserves
// Ægætis, Mötörhead, straße, etc.; Soulseek users mostly type the
// spelled-out form (aegetis, motorhead, strasse). Applied after
// accentFold so umlauts/acutes are already gone by this point.
var ligatureFold = strings.NewReplacer(
	"æ", "ae", "Æ", "AE",
	"œ", "oe", "Œ", "OE",
	"ß", "ss",
	"ø", "o", "Ø", "O",
	"ð", "d", "Ð", "D",
	"þ", "th", "Þ", "Th",
	"ł", "l", "Ł", "L",
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
//   - unicode accents folded to their base ASCII form (é → e,
//     ß → ss via NFD + combining-mark strip)
//   - every non-alphanumeric run replaced with a single space
//   - ASCII case folded (IsLetter keeps unicode letters intact)
//   - leading/trailing whitespace trimmed
//
// Example: "Florence + The Machine (Deluxe)" → "florence the machine"
// Example: "@@user\\Music\\Artist\\01 - Track.flac" → "user music artist 01 track flac"
// Example: "Björk — Ágætis byrjun" → "bjork agaetis byrjun"
func Normalize(s string) string {
	s = parenRE.ReplaceAllString(s, " ")
	// Accent fold up-front so all downstream comparisons are
	// accent-insensitive. transform.String is best-effort — on
	// error fall back to the original string rather than returning
	// empty. (The only realistic failure is pathologically malformed
	// utf-8, which doesn't happen on Discogs/Soulseek inputs in
	// practice.)
	if folded, _, err := transform.String(newAccentFold(), s); err == nil {
		s = folded
	}
	s = ligatureFold.Replace(s)
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

// splitRE separates a compound Discogs title into its parts so we
// can accept a filename that matches ANY part. Three conventions:
//
//   - " / "  A-side/B-side ("Around The World / Primavera"),
//     split / compilation joins. We intentionally match only
//     whitespace-wrapped "/" so in-word slashes (dates, paths,
//     "w/", "A/B") stay as one token.
//   - " - "  subtitle-marker for expanded track names
//     ("One More Virgin -Tracks For Celebration Of New Century-").
//     Peers almost always share the short head, not the full label.
//   - " : "  colon-joined series markers
//     ("Discover The Video: Volume 1" vs. filename "Discover The Video").
//     Most Soulseek peers drop the subtitle after the colon.
//
// A filename matching any one of the split parts is accepted (see
// TitleVariants + ContainsAny).
var splitRE = regexp.MustCompile(`\s+[/\-:]\s+`)

// TitleVariants returns the set of acceptable match-tokens for a
// release/track title. Most titles produce exactly one variant
// (the Tokens(title) result). Discogs conventions that split one
// release into multiple titles — A-side / B-side singles, compilation
// joins, and multi-artist packs — use " / " (with surrounding
// whitespace) as a separator; for these we return one variant per
// slash-part. A filename that fully matches ANY variant is
// considered a match (see ContainsAny).
//
// Examples:
//
//   - "One More Time"               → [[one more time]]
//   - "Around The World / Primavera"→ [[around world] [primavera]]
//   - "Homework / Discovery / Alive 1997"
//     → [[homework] [discovery] [alive 1997]]
//   - "Voyager / It's Yours"        → [[voyager] [it s yours]]
//   - "Le Privé (Avignon/Fr) - 18/11/1995" →
//     [[le privé avignon fr 18 11 1995]]
//     (no spaced " / " → single variant; the path + date slashes
//     don't fragment the match)
//
// Variants whose token list is empty (all stopwords / degenerate)
// are dropped. If every slash-part is degenerate, the slice is
// empty — callers treat that as "no signal to match on", same
// contract as Tokens returning an empty slice.
func TitleVariants(title string) [][]string {
	parts := splitRE.Split(title, -1)
	if len(parts) == 1 {
		t := Tokens(title)
		if len(t) == 0 {
			return nil
		}
		return [][]string{t}
	}
	out := make([][]string, 0, len(parts))
	for _, p := range parts {
		t := Tokens(p)
		if len(t) == 0 {
			continue
		}
		out = append(out, t)
	}
	return out
}

// ContainsAny reports whether `filename` fully matches at least one
// of `variants` — each variant is a token list whose every token
// must appear as a word in Normalize(filename). An empty `variants`
// slice returns true (no signal to filter on).
//
// The filename is normalized + tokenized once per call and reused
// across variants, so matching against N variants is near-free
// versus matching against one.
func ContainsAny(filename string, variants [][]string) bool {
	if len(variants) == 0 {
		return true
	}
	fileSet := make(map[string]struct{}, 16)
	for _, t := range strings.Fields(Normalize(filename)) {
		fileSet[t] = struct{}{}
	}
variant:
	for _, v := range variants {
		if len(v) == 0 {
			continue
		}
		for _, tok := range v {
			if _, ok := fileSet[tok]; !ok {
				continue variant
			}
		}
		return true
	}
	return false
}
