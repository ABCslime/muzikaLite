package download

import (
	"strings"
	"testing"

	"github.com/macabc/muzika/internal/soulseek"
)

var defaultGate = GateConfig{
	MinBitrateKbps: 192,
	MinFileBytes:   2_000_000,
	MaxFileBytes:   200_000_000,
	PeerMaxQueue:   50,
}

func TestClassify_BitrateFloor(t *testing.T) {
	v := classify(soulseek.SearchResult{
		Peer: "p", Filename: "x.mp3", Size: 5_000_000, Bitrate: 96, QueueLen: 0,
	}, defaultGate)
	if v.Pass {
		t.Fatal("96 kbps should fail under 192 floor")
	}
	if !strings.Contains(v.Reason, "bitrate") {
		t.Errorf("reason should mention bitrate: %q", v.Reason)
	}
}

func TestClassify_BitrateZeroIsPass(t *testing.T) {
	// gosk leaves Bitrate=0 when the wire message omits it. Treat as unknown,
	// pass the gate on that axis.
	v := classify(soulseek.SearchResult{
		Peer: "p", Filename: "x.mp3", Size: 5_000_000, Bitrate: 0, QueueLen: 0,
	}, defaultGate)
	if !v.Pass {
		t.Fatalf("bitrate=0 should be treated as unknown and pass, got %q", v.Reason)
	}
}

func TestClassify_SizeFloor(t *testing.T) {
	v := classify(soulseek.SearchResult{
		Peer: "p", Filename: "x.mp3", Size: 1_000_000, Bitrate: 320, QueueLen: 0,
	}, defaultGate)
	if v.Pass {
		t.Fatal("1 MB should fail under 2 MB floor")
	}
	if !strings.Contains(v.Reason, "size") {
		t.Errorf("reason should mention size: %q", v.Reason)
	}
}

func TestClassify_SizeCeiling(t *testing.T) {
	v := classify(soulseek.SearchResult{
		Peer: "p", Filename: "x.flac", Size: 500_000_000, Bitrate: 1000, QueueLen: 0,
	}, defaultGate)
	if v.Pass {
		t.Fatal("500 MB should fail over 200 MB ceiling")
	}
}

func TestClassify_PeerQueueCeiling(t *testing.T) {
	v := classify(soulseek.SearchResult{
		Peer: "p", Filename: "x.mp3", Size: 5_000_000, Bitrate: 320, QueueLen: 999,
	}, defaultGate)
	if v.Pass {
		t.Fatal("queue=999 should fail under 50 ceiling")
	}
}

func TestClassify_GoodResultPasses(t *testing.T) {
	v := classify(soulseek.SearchResult{
		Peer: "p", Filename: "good.flac", Size: 20_000_000, Bitrate: 1024, QueueLen: 3,
	}, defaultGate)
	if !v.Pass {
		t.Fatalf("expected pass, got reject %q", v.Reason)
	}
}

func TestFilterGate_CodecOrder(t *testing.T) {
	// All pass the gate. Sort should put flac ahead of mp3 ahead of ogg.
	in := []soulseek.SearchResult{
		{Peer: "a", Filename: "song.mp3", Size: 5_000_000, Bitrate: 320},
		{Peer: "b", Filename: "song.flac", Size: 25_000_000, Bitrate: 1000},
		{Peer: "c", Filename: "song.ogg", Size: 5_000_000, Bitrate: 256},
	}
	passed, _ := filterGate(in, defaultGate)
	if len(passed) != 3 {
		t.Fatalf("expected 3 pass, got %d", len(passed))
	}
	got := []string{passed[0].Filename, passed[1].Filename, passed[2].Filename}
	want := []string{"song.flac", "song.mp3", "song.ogg"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("position %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestFilterGate_BitrateTieBreak(t *testing.T) {
	// Two mp3s, higher bitrate wins.
	in := []soulseek.SearchResult{
		{Peer: "low", Filename: "a.mp3", Size: 5_000_000, Bitrate: 192, QueueLen: 0},
		{Peer: "hi", Filename: "b.mp3", Size: 5_000_000, Bitrate: 320, QueueLen: 0},
	}
	passed, _ := filterGate(in, defaultGate)
	if len(passed) != 2 || passed[0].Peer != "hi" {
		t.Errorf("expected 'hi' first, got %+v", passed)
	}
}

func TestFilterGate_QueueTieBreak(t *testing.T) {
	in := []soulseek.SearchResult{
		{Peer: "busy", Filename: "a.mp3", Size: 5_000_000, Bitrate: 320, QueueLen: 40},
		{Peer: "free", Filename: "b.mp3", Size: 5_000_000, Bitrate: 320, QueueLen: 0},
	}
	passed, _ := filterGate(in, defaultGate)
	if len(passed) != 2 || passed[0].Peer != "free" {
		t.Errorf("expected 'free' first, got %+v", passed)
	}
}

func TestFilterGate_VerdictsRecordEveryResult(t *testing.T) {
	in := []soulseek.SearchResult{
		{Peer: "ok", Filename: "a.flac", Size: 20_000_000, Bitrate: 1000},
		{Peer: "bad", Filename: "b.mp3", Size: 5_000_000, Bitrate: 96},
	}
	_, verdicts := filterGate(in, defaultGate)
	if len(verdicts) != 2 {
		t.Fatalf("expected 2 verdicts, got %d", len(verdicts))
	}
	if !verdicts[0].Pass || verdicts[1].Pass {
		t.Errorf("verdicts wrong: %+v", verdicts)
	}
}

func TestRelax_HalvesThresholds(t *testing.T) {
	r := defaultGate.Relax()
	if r.MinBitrateKbps != 96 {
		t.Errorf("bitrate relax: %d, want 96", r.MinBitrateKbps)
	}
	if r.MinFileBytes != 1_000_000 {
		t.Errorf("min size relax: %d, want 1M", r.MinFileBytes)
	}
	if r.MaxFileBytes != 400_000_000 {
		t.Errorf("max size relax: %d, want 400M (doubled)", r.MaxFileBytes)
	}
	if r.PeerMaxQueue != 100 {
		t.Errorf("queue relax: %d, want 100 (doubled)", r.PeerMaxQueue)
	}
}

func TestRelax_NowAccepts96kbps(t *testing.T) {
	r := defaultGate.Relax()
	v := classify(soulseek.SearchResult{
		Peer: "p", Filename: "x.mp3", Size: 5_000_000, Bitrate: 96, QueueLen: 0,
	}, r)
	if !v.Pass {
		t.Errorf("relaxed gate should accept 96 kbps, got %q", v.Reason)
	}
}
