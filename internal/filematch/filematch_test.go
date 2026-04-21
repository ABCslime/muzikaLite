package filematch_test

import (
	"reflect"
	"testing"

	"github.com/macabc/muzika/internal/filematch"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"Hello World", "hello world"},
		{"Florence + The Machine", "florence the machine"},
		{"Discovery (Remastered)", "discovery"},
		{"Daft Punk - Discovery [WPCR-80083] (JP)", "daft punk discovery"},
		{"@@host\\Music\\Artist\\01 - Track.flac", "host music artist 01 track flac"},
		{"  multiple   spaces  ", "multiple spaces"},
		// v0.4.2 PR E (post-QA): accent-fold so Discogs's Björk matches
		// the ASCII "bjork" that Soulseek users actually type.
		{"Björk — Ágætis byrjun", "bjork agaetis byrjun"},
		{"Le Privé (Avignon/Fr) - 18/11/1995", "le prive 18 11 1995"},
		{"A & B, C/D", "a b c d"},
		{"()", ""},
		{"Song (v2) [2020]", "song"},
	}
	for _, c := range cases {
		got := filematch.Normalize(c.in)
		if got != c.want {
			t.Errorf("Normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTokens(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"The Wall", []string{"wall"}},                                // stopword dropped
		{"A Hard Day's Night", []string{"hard", "day", "s", "night"}}, // "a" dropped
		{"Never Let Me Go", []string{"never", "let", "me", "go"}},
		{"of the in on at", nil}, // all stopwords → empty
		{"Discovery (Remastered)", []string{"discovery"}},
		{"Florence + The Machine", []string{"florence", "machine"}},
	}
	for _, c := range cases {
		got := filematch.Tokens(c.in)
		if c.want == nil {
			if len(got) != 0 {
				t.Errorf("Tokens(%q) = %v, want empty", c.in, got)
			}
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Tokens(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestContains(t *testing.T) {
	cases := []struct {
		filename string
		tokens   []string
		want     bool
	}{
		// Empty tokens = trivially matches.
		{"Whatever", nil, true},
		{"", nil, true},

		// Exact match
		{"Daft Punk - Discovery.flac", []string{"discovery"}, true},
		{"Daft Punk - Discovery.flac", []string{"daft", "discovery"}, true},
		{"Daft Punk - Discovery.flac", []string{"homework"}, false},

		// Filename with path separators.
		{"@@host\\Music\\Daft Punk\\01 - One More Time.flac",
			[]string{"one", "more", "time"}, true},

		// Case and punctuation differences between filename and tokens.
		{"FLORENCE - Never.Let.Me.Go.mp3",
			[]string{"florence", "never", "let", "me", "go"}, true},

		// Short-token false-positive guard. "aa" must match as a WORD,
		// not a substring of "kawasaki".
		{"Kawasaki.mp3", []string{"aa"}, false},
		{"Merzbow - Sha Mo 3000 - Aa.mp3", []string{"aa"}, true},

		// Parens in filename are stripped at normalize time, so tokens
		// can still match across them.
		{"Discovery (Deluxe Edition).flac", []string{"discovery"}, true},

		// Missing token fails.
		{"Florence - Let Me Go.mp3", []string{"never", "let", "me", "go"}, false},
	}
	for _, c := range cases {
		got := filematch.Contains(c.filename, c.tokens)
		if got != c.want {
			t.Errorf("Contains(%q, %v) = %v, want %v", c.filename, c.tokens, got, c.want)
		}
	}
}

func TestTitleVariants(t *testing.T) {
	cases := []struct {
		in   string
		want [][]string
	}{
		{"One More Time", [][]string{{"one", "more", "time"}}},
		// Slash-separated (with surrounding whitespace) = one variant per side.
		{"Around The World / Primavera",
			[][]string{{"around", "world"}, {"primavera"}}},
		{"Homework / Discovery / Alive 1997",
			[][]string{{"homework"}, {"discovery"}, {"alive", "1997"}}},
		// A side with only stopwords gets dropped.
		{"The / Real Title",
			[][]string{{"real", "title"}}},
		// Subtitle dash ("Head - Subtitle") — peers share the head.
		{"One More Virgin - Tracks For Celebration Of New Century",
			[][]string{{"one", "more", "virgin"},
				{"tracks", "celebration", "new", "century"}}},
		// Colon series marker — peers drop the subtitle.
		{"Discover The Video : Volume 1",
			[][]string{{"discover", "video"}, {"volume", "1"}}},
		// In-word slashes (dates, paths, fractions) don't split.
		// The " - " separator DOES split the head ("Le Privé ...")
		// from the date suffix — peers that name this as just
		// "Le Prive" (without the date) still match via the head variant.
		{"Le Privé (Avignon/Fr) - 18/11/1995",
			[][]string{{"le", "prive"}, {"18", "11", "1995"}}},
		// ("a" is a stopword and drops out.)
		{"A/B Testing",
			[][]string{{"b", "testing"}}},
		// Empty / degenerate returns nil.
		{"", nil},
		{"the of a", nil},
		{" / ", nil},
	}
	for _, c := range cases {
		got := filematch.TitleVariants(c.in)
		if c.want == nil {
			if len(got) != 0 {
				t.Errorf("TitleVariants(%q) = %v, want empty", c.in, got)
			}
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("TitleVariants(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestContainsAny(t *testing.T) {
	cases := []struct {
		filename string
		variants [][]string
		want     bool
	}{
		// Empty variants = trivially matches.
		{"whatever.mp3", nil, true},
		{"whatever.mp3", [][]string{}, true},

		// Single variant.
		{"Daft Punk - Around The World.mp3",
			[][]string{{"around", "world"}}, true},

		// Multiple variants — split release "Around The World / Primavera".
		// A filename of just the A-side matches because one variant fits.
		{"Daft Punk - Around The World.mp3",
			[][]string{{"around", "world"}, {"primavera"}}, true},
		// B-side-only filename also matches.
		{"Marina Rei - Primavera.mp3",
			[][]string{{"around", "world"}, {"primavera"}}, true},
		// Partial match on a variant (missing "world") + no hit on the
		// other → false.
		{"Around the Corner.mp3",
			[][]string{{"around", "world"}, {"primavera"}}, false},

		// Degenerate variant (empty token list) is skipped.
		{"foo.mp3",
			[][]string{{}, {"foo"}}, true},
	}
	for _, c := range cases {
		got := filematch.ContainsAny(c.filename, c.variants)
		if got != c.want {
			t.Errorf("ContainsAny(%q, %v) = %v, want %v",
				c.filename, c.variants, got, c.want)
		}
	}
}

func TestMatchesTitle(t *testing.T) {
	cases := []struct {
		filename, title string
		want            bool
	}{
		// Happy paths.
		{"Daft Punk - Discovery - 01 One More Time.flac", "One More Time", true},
		{"Florence + The Machine - Never Let Me Go (2012).mp3", "Never Let Me Go", true},
		{"Merzbow - Sha Mo 3000 - Aa.mp3", "Aa", true},

		// Different song by same artist — no match.
		{"Daft Punk - Homework - 01 Revolution 909.flac", "One More Time", false},

		// Different artist, same title — still matches (artist check
		// is a separate concern, handled in download/worker.go).
		{"Josh Groban - Never Let Me Go.mp3", "Never Let Me Go", true},

		// Remastered vs original — matches (parens stripped on both).
		{"Discovery (Remastered 2021).flac", "Discovery (2001)", true},

		// Stopwords: "The Wall" title, "Wall" in filename.
		{"Pink Floyd - Wall.mp3", "The Wall", true},

		// Filename fragment lacks the core title word.
		{"Pink Floyd - Comfortably Numb.mp3", "The Wall", false},
	}
	for _, c := range cases {
		got := filematch.MatchesTitle(c.filename, c.title)
		if got != c.want {
			t.Errorf("MatchesTitle(%q, %q) = %v, want %v",
				c.filename, c.title, got, c.want)
		}
	}
}
