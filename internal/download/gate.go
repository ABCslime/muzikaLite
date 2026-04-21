package download

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/macabc/muzika/internal/soulseek"
)

// GateConfig parametrizes the quality gate. Populated from config.Config
// in main.go; tests construct it directly.
//
// ROADMAP §v0.4 item 3: floors 192 kbps, 2 MB, 200 MB, peer queue ≤ 50.
// Codec preference flac > mp3 > other. Every pass/fail logged with reason.
type GateConfig struct {
	MinBitrateKbps int
	MinFileBytes   int64
	MaxFileBytes   int64
	PeerMaxQueue   int
}

// GateMode is either strict (the ROADMAP thresholds) or relaxed (halved
// thresholds, used as a one-shot fallback after strict rejects everything
// at every ladder rung).
//
// PR 2 implements a single, undifferentiated relax path. PR 3 adds the
// origin-aware split where user-initiated search surfaces the relaxation
// to the caller ("no high-quality matches; showing best available") while
// passive refill stays silent.
type GateMode string

const (
	GateModeStrict  GateMode = "strict"
	GateModeRelaxed GateMode = "relaxed"
)

// Relax returns a config with every numeric floor halved (and the max
// raised 2x for size). Used for the fallback pass.
func (g GateConfig) Relax() GateConfig {
	return GateConfig{
		MinBitrateKbps: g.MinBitrateKbps / 2,
		MinFileBytes:   g.MinFileBytes / 2,
		MaxFileBytes:   g.MaxFileBytes * 2,
		PeerMaxQueue:   g.PeerMaxQueue * 2,
	}
}

// GateVerdict is one result's pass/fail decision plus a human-readable
// reason for discovery_log. ranksort() uses (codec, bitrate, queue) to
// break ties when multiple results pass the gate.
type GateVerdict struct {
	Result soulseek.SearchResult
	Pass   bool
	Reason string // populated on Pass=false; empty on Pass=true
}

// classify applies every threshold to one result. Returns the verdict.
//
// Bitrate == 0 and FilesShared == 0 are treated as "unknown" rather than
// "zero-valued" — the gosk backend leaves these at 0 when the Soulseek
// wire-level response omits them. Rejecting on 0 would silently drop
// every result.
func classify(r soulseek.SearchResult, g GateConfig) GateVerdict {
	if g.MinBitrateKbps > 0 && r.Bitrate > 0 && r.Bitrate < g.MinBitrateKbps {
		return GateVerdict{Result: r, Pass: false,
			Reason: fmt.Sprintf("bitrate %d < %d", r.Bitrate, g.MinBitrateKbps)}
	}
	if g.MinFileBytes > 0 && r.Size > 0 && r.Size < g.MinFileBytes {
		return GateVerdict{Result: r, Pass: false,
			Reason: fmt.Sprintf("size %d < %d", r.Size, g.MinFileBytes)}
	}
	if g.MaxFileBytes > 0 && r.Size > g.MaxFileBytes {
		return GateVerdict{Result: r, Pass: false,
			Reason: fmt.Sprintf("size %d > %d", r.Size, g.MaxFileBytes)}
	}
	if g.PeerMaxQueue > 0 && r.QueueLen > g.PeerMaxQueue {
		return GateVerdict{Result: r, Pass: false,
			Reason: fmt.Sprintf("peer queue %d > %d", r.QueueLen, g.PeerMaxQueue)}
	}
	return GateVerdict{Result: r, Pass: true}
}

// filterGate returns the subset of results that pass the gate, and the
// full verdict list (for observability logging). The filtered slice is
// sorted by (codec preference, bitrate desc, queue asc) so the head is
// the best candidate.
func filterGate(results []soulseek.SearchResult, g GateConfig) ([]soulseek.SearchResult, []GateVerdict) {
	verdicts := make([]GateVerdict, 0, len(results))
	passed := make([]soulseek.SearchResult, 0, len(results))
	for _, r := range results {
		v := classify(r, g)
		verdicts = append(verdicts, v)
		if v.Pass {
			passed = append(passed, r)
		}
	}
	ranksort(passed)
	return passed, verdicts
}

// codecRank orders codec preference: flac > mp3 > other. Lower rank wins.
func codecRank(filename string) int {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".flac":
		return 0
	case ".mp3":
		return 1
	default:
		return 2
	}
}

// ranksort sorts results in place: best candidate first.
//
// Order: codec rank asc (flac first), then bitrate desc, then peer queue asc.
// Unknown bitrates (0) sort last within a codec. FilesShared was previously a
// tiebreaker but the gosk backend leaves it at 0, so it's not useful here.
func ranksort(results []soulseek.SearchResult) {
	sort.SliceStable(results, func(i, j int) bool {
		ci, cj := codecRank(results[i].Filename), codecRank(results[j].Filename)
		if ci != cj {
			return ci < cj
		}
		bi, bj := results[i].Bitrate, results[j].Bitrate
		// Treat 0 as lowest (sort after real bitrates).
		if (bi == 0) != (bj == 0) {
			return bj == 0
		}
		if bi != bj {
			return bi > bj
		}
		return results[i].QueueLen < results[j].QueueLen
	})
}

// pickFromPassed returns the head of a gate-filtered (and ranksort-sorted)
// slice as the chosen peer+file. The split lets callers retain the full
// pass-list for observability / future "second-choice retry".
func pickFromPassed(passed []soulseek.SearchResult) (string, soulseek.SearchResult, bool) {
	if len(passed) == 0 {
		return "", soulseek.SearchResult{}, false
	}
	return passed[0].Peer, passed[0], true
}
