package bus

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestDispatchByType_LegacyRequestRandomSong verifies that a pre-v0.4 outbox
// row with event_type="RequestRandomSong" decodes into a DiscoveryIntent and
// republishes with Strategy=StrategyRandom. This is the outbox backward-compat
// promise made in ROADMAP §v0.4 PR 1.
//
// Request events were never routed through the outbox by design, but any
// straggler row from an ad-hoc insert or pre-v0.4 migration window would be
// treated as poison and dropped under the default unknown-type case. The
// backward-compat path turns such rows into well-formed DiscoveryIntents
// instead of silently deleting them.
func TestDispatchByType_LegacyRequestRandomSong(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := New(64, log)

	// Subscribe before dispatch so we observe the republish.
	ch := Subscribe[DiscoveryIntent](b, "test/legacy-roundtrip")

	// An OutboxDispatcher with a nil *sql.DB is safe here because
	// dispatchByType's RequestRandomSong case only unmarshals + publishes;
	// it never touches the DB. The dispatcher's other machinery (run,
	// drainOnce) is not exercised by this test.
	d := &OutboxDispatcher{bus: b, log: log}

	legacyPayload := map[string]any{
		"song_id": uuid.New().String(),
		"user_id": uuid.New().String(),
		"genre":   "electronic",
	}
	raw, err := json.Marshal(legacyPayload)
	if err != nil {
		t.Fatalf("marshal legacy: %v", err)
	}

	if err := d.dispatchByType(context.Background(), TypeRequestRandomSong, raw); err != nil {
		t.Fatalf("dispatchByType: %v", err)
	}

	select {
	case ev := <-ch:
		if ev.Strategy != StrategyRandom {
			t.Errorf("Strategy=%q, want %q (backfill failed)", ev.Strategy, StrategyRandom)
		}
		if ev.Genre != "electronic" {
			t.Errorf("Genre=%q, want electronic", ev.Genre)
		}
		if ev.SongID.String() != legacyPayload["song_id"] {
			t.Errorf("SongID round-trip failed: got %v, want %v", ev.SongID, legacyPayload["song_id"])
		}
		if ev.UserID.String() != legacyPayload["user_id"] {
			t.Errorf("UserID round-trip failed: got %v, want %v", ev.UserID, legacyPayload["user_id"])
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("legacy payload did not republish as DiscoveryIntent")
	}
}

// TestDispatchByType_DiscoveryIntent verifies the forward path: a
// TypeDiscoveryIntent outbox row round-trips with all fields intact.
// No current publisher outboxes DiscoveryIntent (it's a request event), but
// the case is wired for parity with the legacy path and for any future
// state-change discovery event.
func TestDispatchByType_DiscoveryIntent(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := New(64, log)

	ch := Subscribe[DiscoveryIntent](b, "test/forward-roundtrip")
	d := &OutboxDispatcher{bus: b, log: log}

	seed := uuid.New()
	intent := DiscoveryIntent{
		SongID:     uuid.New(),
		UserID:     uuid.New(),
		Strategy:   StrategySearch,
		Query:      "shanti people",
		SeedSongID: seed,
	}
	raw, err := json.Marshal(intent)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if err := d.dispatchByType(context.Background(), TypeDiscoveryIntent, raw); err != nil {
		t.Fatalf("dispatchByType: %v", err)
	}

	select {
	case ev := <-ch:
		if ev.Strategy != StrategySearch {
			t.Errorf("Strategy=%q, want %q", ev.Strategy, StrategySearch)
		}
		if ev.Query != "shanti people" {
			t.Errorf("Query=%q, want %q", ev.Query, "shanti people")
		}
		if ev.SeedSongID != seed {
			t.Errorf("SeedSongID=%v, want %v", ev.SeedSongID, seed)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("DiscoveryIntent did not round-trip")
	}
}

// TestDispatchByType_Unknown verifies unknown event types still error (the
// poisoned-row path in drainOnce relies on this to delete-and-move-on).
func TestDispatchByType_Unknown(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := New(64, log)
	d := &OutboxDispatcher{bus: b, log: log}

	err := d.dispatchByType(context.Background(), "SomethingNobodyKnows", []byte("{}"))
	if err == nil {
		t.Fatal("expected error for unknown event type, got nil")
	}
}
