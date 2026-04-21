package queue

import "testing"

func TestNormalizeQuery(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"Shanti People", "shanti people"},
		{"  leading trailing   ", "leading trailing"},
		{"already-lowercase", "already lowercase"},
		{"Björk — Post!", "björk post"},
		{"Boards Of Canada (1998)", "boards of canada 1998"},
		{"multi    space\ttab\nnewline", "multi space tab newline"},
		{"¿«¡!! ???", ""},
		{"DJ Shadow – Endtroducing.....", "dj shadow endtroducing"},
		// Unicode letters beyond ASCII preserved + lowercased.
		{"Sigur Rós — Ágætis byrjun", "sigur rós ágætis byrjun"},
		// Numbers are kept.
		{"Aphex Twin - I Care Because You Do (1995)", "aphex twin i care because you do 1995"},
	}
	for _, c := range cases {
		got := normalizeQuery(c.in)
		if got != c.want {
			t.Errorf("normalizeQuery(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRetryLongWords(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"the cat in a box", ""},                    // nothing > 4 chars
		{"shanti people", "shanti people"},          // both qualify
		{"the quick brown fox", "quick brown"},       // short words dropped
		{"aphex twin i care because you do 1995", "aphex because"},
		{"boards of canada 1998", "boards canada"},
		{"björk post", "björk"}, // björk is 5 runes; post is 4 (dropped)
	}
	for _, c := range cases {
		got := retryLongWords(c.in)
		if got != c.want {
			t.Errorf("retryLongWords(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
