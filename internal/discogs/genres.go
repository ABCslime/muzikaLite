package discogs

import "strings"

// GenreKind distinguishes Discogs' two overlapping taxonomies.
//
// Discogs catalogues a release with BOTH a small top-level Genre
// (Electronic, Rock, Jazz, ~15 total) AND one or more finer-grained
// Styles (House, Techno, Trance, Bebop, ~300 total). Its /database/
// search endpoint accepts them on distinct query parameters — `genre=X`
// matches a top-level only, `style=X` matches a style only. Passing
// one name as the wrong kind yields zero results.
//
// v0.4.2 PR B.1: pinning "Techno" or "House" has to route through
// `style=` to produce any results. KindStyle entries tell the Discogs
// client to do that; KindGenre entries go to `genre=` as before.
type GenreKind string

const (
	KindGenre GenreKind = "genre"
	KindStyle GenreKind = "style"
)

// GenreEntry is one Discogs vocabulary term plus its kind. Exposed so
// the search preview package can match the same list the Discogs
// client routes against — single source of truth avoids the drift
// we'd otherwise get between "what users see in dropdown/settings"
// and "what the backend sends to Discogs."
type GenreEntry struct {
	Name string    // human-readable, Discogs-canonical casing
	Kind GenreKind // genre or style — determines query param
}

// GenreVocabulary returns the curated list of genres + popular styles
// muzika surfaces in its UI (Settings checkbox grid, TopBar dropdown).
//
// Curation criteria:
//   - All 15 top-level Discogs genres (KindGenre) — the full set, so
//     Settings stays comprehensive at the coarse level.
//   - Popular styles under Electronic, Rock, Hip Hop, Jazz — the ones
//     we expect users to actually type into the search box. Not the
//     full ~300 Discogs styles; deep cuts stay discoverable via the
//     search endpoint's dropdown matching (any substring hit still
//     surfaces an unlisted style, it just won't be checkable in
//     Settings). Extend this list when you find a style you wanted
//     to pin and couldn't.
//
// Returns a NEW slice each call so callers can't mutate the canonical
// vocabulary in place.
func GenreVocabulary() []GenreEntry {
	return append([]GenreEntry(nil), genreVocabulary...)
}

// genreVocabulary is the package-private canonical list. Kept private
// so consumers can't mutate; GenreVocabulary returns a defensive copy.
var genreVocabulary = []GenreEntry{
	// --- Top-level genres (the complete Discogs set) ---
	{"Blues", KindGenre},
	{"Brass & Military", KindGenre},
	{"Children's", KindGenre},
	{"Classical", KindGenre},
	{"Electronic", KindGenre},
	{"Folk, World, & Country", KindGenre},
	{"Funk / Soul", KindGenre},
	{"Hip Hop", KindGenre},
	{"Jazz", KindGenre},
	{"Latin", KindGenre},
	{"Non-Music", KindGenre},
	{"Pop", KindGenre},
	{"Reggae", KindGenre},
	{"Rock", KindGenre},
	{"Stage & Screen", KindGenre},

	// --- Electronic styles (the ones people actually type) ---
	{"House", KindStyle},
	{"Deep House", KindStyle},
	{"Tech House", KindStyle},
	{"Acid House", KindStyle},
	{"Progressive House", KindStyle},
	{"Minimal", KindStyle},
	{"Techno", KindStyle},
	{"Trance", KindStyle},
	{"Psy-Trance", KindStyle},
	{"Progressive Trance", KindStyle},
	{"Ambient", KindStyle},
	{"Drum n Bass", KindStyle},
	{"Dubstep", KindStyle},
	{"Dub", KindStyle},
	{"Breakbeat", KindStyle},
	{"Electro", KindStyle},
	{"IDM", KindStyle},
	{"Downtempo", KindStyle},
	{"Disco", KindStyle},
	{"Synth-pop", KindStyle},
	{"UK Garage", KindStyle},
	{"Hardstyle", KindStyle},
	{"Vaporwave", KindStyle},
	{"Lo-Fi", KindStyle},
	{"Experimental", KindStyle},

	// --- Rock styles ---
	{"Alternative Rock", KindStyle},
	{"Indie Rock", KindStyle},
	{"Punk", KindStyle},
	{"Post-Punk", KindStyle},
	{"Heavy Metal", KindStyle},
	{"Prog Rock", KindStyle},
	{"Psychedelic Rock", KindStyle},
	{"Shoegaze", KindStyle},
	{"Grunge", KindStyle},

	// --- Hip Hop styles ---
	{"Boom Bap", KindStyle},
	{"Trap", KindStyle},
	{"Conscious", KindStyle},
	{"Instrumental", KindStyle},

	// --- Jazz styles ---
	{"Bebop", KindStyle},
	{"Cool Jazz", KindStyle},
	{"Fusion", KindStyle},
	{"Hard Bop", KindStyle},
	{"Soul-Jazz", KindStyle},

	// --- Funk/Soul styles ---
	{"Funk", KindStyle},
	{"Soul", KindStyle},
}

// kindIndex is a lazy-initialized lower-case lookup table; a module-
// level var rather than a sync.Once because the vocabulary is static.
var kindIndex = func() map[string]GenreKind {
	m := make(map[string]GenreKind, len(genreVocabulary))
	for _, e := range genreVocabulary {
		m[strings.ToLower(e.Name)] = e.Kind
	}
	return m
}()

// KindOf returns the Discogs query-param kind for a name. Unknown
// names (anything not in the curated vocabulary) default to KindGenre
// — that's the pre-v0.4.2 PR B.1 behavior and what tests expect when
// a user pins something uncurated via a direct settings edit.
func KindOf(name string) GenreKind {
	if k, ok := kindIndex[strings.ToLower(strings.TrimSpace(name))]; ok {
		return k
	}
	return KindGenre
}
