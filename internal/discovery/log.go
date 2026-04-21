// Package discovery writes the discovery_log table — a per-attempt audit of
// every seeder pick and every download-ladder rung. Never deleted.
// ROADMAP §v0.4 item 7.
//
// The writer is synchronous: one INSERT per Record() call on the shared
// single-writer *sql.DB. Volume is low (~10 rows per song acquired), SQLite
// INSERTs are microseconds, and keeping the path synchronous avoids a
// second goroutine that would need its own shutdown coordination. If
// throughput ever matters, batch via the outbox pattern; for now, simpler.
//
// Consumers in bandcamp/, discogs/, and download/ take a *Writer in their
// NewService constructor. A nil *Writer is tolerated — Record() becomes a
// no-op — so tests that don't care about observability pass nil.
package discovery

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/google/uuid"
)

// Source names the module emitting the log row.
type Source string

const (
	SourceBandcamp Source = "bandcamp"
	SourceDiscogs  Source = "discogs"
	SourceDownload Source = "download"
)

// Stage names where in the pipeline the row was emitted.
//
// Seed/SeedNoResults — seeder picked (or failed to pick) a (title, artist) pair.
// Ladder — one Soulseek search-ladder rung ran and returned raw results.
// Gate — the quality gate accepted or rejected a rung's results.
// Picked — a specific peer + file was chosen and a download was initiated.
// Failed — terminal failure for this DiscoveryIntent's download.
type Stage string

const (
	StageSeed   Stage = "seed"
	StageLadder Stage = "ladder"
	StageGate   Stage = "gate"
	StagePicked Stage = "picked"
	StageFailed Stage = "failed"
)

// Outcome summarizes what happened at this stage.
type Outcome string

const (
	OutcomeOK              Outcome = "ok"
	OutcomeNoResults       Outcome = "no_results"
	OutcomeRejectedStrict  Outcome = "rejected_strict"
	OutcomeRelaxed         Outcome = "relaxed"
	OutcomeError           Outcome = "error"
	OutcomeRateLimited     Outcome = "rate_limited"
)

// Record is the shape of one discovery_log row. Fields are nullable in SQL
// and zero-valued here; Writer translates zero values to SQL NULL for
// nullable columns.
type Record struct {
	SongID   uuid.UUID // uuid.Nil → NULL
	UserID   uuid.UUID // uuid.Nil → NULL
	Source   Source    // required
	Strategy string    // DiscoveryIntent.Strategy, or "" when not applicable
	Stage    Stage     // required
	Rung     int       // -1 → NULL (sentinel; 0/1/2 are the real ladder rungs)
	Query    string    // "" → NULL
	Outcome  Outcome   // required
	Reason   string    // free-form; "" → NULL

	ResultCount int    // -1 → NULL
	Filename    string // "" → NULL
	Peer        string // "" → NULL
	Bitrate     int    // 0 → NULL
	Size        int64  // 0 → NULL
}

// Writer inserts Records into discovery_log.
type Writer struct {
	db  *sql.DB
	log *slog.Logger
}

// NewWriter returns a Writer that inserts into db. Pass nil *sql.DB to
// disable the writer entirely (Record becomes a no-op).
func NewWriter(db *sql.DB) *Writer {
	return &Writer{
		db:  db,
		log: slog.Default().With("mod", "discovery"),
	}
}

// Record inserts one row. Errors are logged (not returned) because the
// discovery log is advisory — an observability failure must never block
// the acquisition pipeline.
//
// A nil *Writer is a valid receiver for tests; the call is a no-op.
func (w *Writer) Record(ctx context.Context, rec Record) {
	if w == nil || w.db == nil {
		return
	}
	_, err := w.db.ExecContext(ctx, `
		INSERT INTO discovery_log (
			song_id, user_id,
			source, strategy, stage, rung, query,
			outcome, reason, result_count,
			filename, peer, bitrate, size
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		nullUUID(rec.SongID),
		nullUUID(rec.UserID),
		string(rec.Source),
		nullString(rec.Strategy),
		string(rec.Stage),
		nullRung(rec.Rung),
		nullString(rec.Query),
		string(rec.Outcome),
		nullString(rec.Reason),
		nullInt(rec.ResultCount),
		nullString(rec.Filename),
		nullString(rec.Peer),
		nullInt(rec.Bitrate),
		nullInt64(rec.Size),
	)
	if err != nil {
		w.log.Error("discovery_log insert failed",
			"err", err,
			"source", rec.Source,
			"stage", rec.Stage,
			"outcome", rec.Outcome)
	}
}

// ---- nullable helpers ----

func nullUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id.String()
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullRung treats -1 as the "no rung applicable" sentinel; the real ladder
// rungs are 0, 1, 2. We can't use 0 as the sentinel because rung 0 (catno)
// is a valid and meaningful value.
func nullRung(r int) any {
	if r < 0 {
		return nil
	}
	return r
}

func nullInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func nullInt64(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}
