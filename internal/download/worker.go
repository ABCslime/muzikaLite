// Package download is a thin wrapper around internal/soulseek.Client. It owns
// the RequestDownload worker pool: search via the Soulseek backend, pick a
// peer, download the file, and emit LoadedSong (via outbox).
//
// The name reflects what the package does — turn "title + artist" into a
// downloaded file — not which backend it speaks to. Swapping gosk for a
// different Soulseek client later wouldn't change this package's API.
//
// v0.4 PR 2 additions:
//
//   - Catalog-number ladder (ROADMAP §v0.4 item 4). Three rungs tried in
//     order — catno, artist+title, title — with the rung window shortening
//     after the first miss to cap worst-case latency. Rung 0 is skipped
//     when RequestDownload.CatalogNumber is empty.
//
//   - Quality gate (ROADMAP §v0.4 item 3). See gate.go. Applied at each
//     rung; the ladder exits at the first rung whose gate-filtered count
//     meets LadderEnoughResults. If every rung rejected strict, one relax
//     pass with halved thresholds runs before the worker gives up.
//
//   - Discovery log (ROADMAP §v0.4 item 7). Every rung, every gate outcome,
//     every picked file writes a discovery_log row. Never deleted.
package download

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/bus"
	"github.com/macabc/muzika/internal/db"
	"github.com/macabc/muzika/internal/discovery"
	"github.com/macabc/muzika/internal/soulseek"
)

// Tunables that aren't operator-facing. Graduate to config if they need to be.
const (
	searchWindow       = 10 * time.Second
	pollInterval       = 2 * time.Second
	downloadMaxTimeout = 5 * time.Minute
)

// Config parametrizes the ladder + gate behavior. Populated from
// config.Config in main.go; tests construct it inline.
//
// The zero value is not sane — callers must set thresholds explicitly.
// DefaultConfig below documents what's reasonable.
type Config struct {
	Gate            GateConfig
	LadderEnabled   bool
	LadderEnough    int
	LadderRungWait  time.Duration
}

// DefaultConfig returns the ROADMAP §v0.4 defaults. Used by tests and as
// the fallback when main.go skips passing a Config (only happens for
// pre-v0.4 test fixtures).
func DefaultConfig() Config {
	return Config{
		Gate: GateConfig{
			MinBitrateKbps: 192,
			MinFileBytes:   2_000_000,
			MaxFileBytes:   200_000_000,
			PeerMaxQueue:   50,
		},
		LadderEnabled:  true,
		LadderEnough:   3,
		LadderRungWait: 5 * time.Second,
	}
}

// Service owns the RequestDownload worker pool.
type Service struct {
	db               *sql.DB
	soulseek         soulseek.Client
	bus              *bus.Bus
	dispatcher       *bus.OutboxDispatcher
	musicStoragePath string
	cfg              Config
	logw             *discovery.Writer
	log              *slog.Logger
}

// NewService wires a Service with the default Config. Tests that want to
// override thresholds use NewServiceWithConfig.
func NewService(
	sqlDB *sql.DB,
	sk soulseek.Client,
	musicStoragePath string,
	b *bus.Bus,
	d *bus.OutboxDispatcher,
) *Service {
	return NewServiceWithConfig(sqlDB, sk, musicStoragePath, b, d, nil, DefaultConfig())
}

// NewServiceWithConfig wires a Service with explicit Config and optional
// discovery writer. The writer may be nil (tests skip observability).
func NewServiceWithConfig(
	sqlDB *sql.DB,
	sk soulseek.Client,
	musicStoragePath string,
	b *bus.Bus,
	d *bus.OutboxDispatcher,
	logw *discovery.Writer,
	cfg Config,
) *Service {
	return &Service{
		db:               sqlDB,
		soulseek:         sk,
		bus:              b,
		dispatcher:       d,
		musicStoragePath: musicStoragePath,
		cfg:              cfg,
		logw:             logw,
		log:              slog.Default().With("mod", "download"),
	}
}

// StartWorkers subscribes to RequestDownload with `workers` goroutines.
func (s *Service) StartWorkers(ctx context.Context, workers int) {
	ch := bus.Subscribe[bus.RequestDownload](s.bus, "download/request-download")
	bus.RunPool(ctx, s.bus, "download/request-download", workers, ch, s.onRequestDownload)
}

// OnRequestDownload is exported for tests.
func (s *Service) OnRequestDownload(ctx context.Context, ev bus.RequestDownload) error {
	return s.onRequestDownload(ctx, ev)
}

func (s *Service) onRequestDownload(ctx context.Context, ev bus.RequestDownload) error {
	peer, file, usedRelax, ok := s.runLadder(ctx, ev)
	if !ok {
		s.recordFailed(ctx, ev, "no viable peer after ladder + gate")
		return s.emitError(ctx, ev.SongID)
	}

	s.logw.Record(ctx, discovery.Record{
		SongID:   ev.SongID,
		Source:   discovery.SourceDownload,
		Strategy: "", // not a seeder-stage record
		Stage:    discovery.StagePicked,
		Outcome:  discovery.OutcomeOK,
		Filename: file.Filename,
		Peer:     peer,
		Bitrate:  file.Bitrate,
		Size:     file.Size,
	})

	h, err := s.soulseek.Download(ctx, peer, file.Filename, file.Size)
	if err != nil {
		s.log.Error("download start failed", "song_id", ev.SongID, "err", err)
		s.recordFailed(ctx, ev, "download start: "+err.Error())
		return s.emitError(ctx, ev.SongID)
	}

	filePath, err := s.waitForDownload(ctx, h)
	if err != nil {
		s.log.Error("download wait failed", "song_id", ev.SongID, "err", err)
		s.recordFailed(ctx, ev, err.Error())
		return s.emitError(ctx, ev.SongID)
	}

	// ROADMAP §v0.4 item 6 — split relaxation by origin. Surface only for
	// user-initiated search (StrategySearch). Passive refill relaxes
	// silently regardless of usedRelax.
	relaxed := usedRelax && ev.Strategy == bus.StrategySearch
	return s.emitCompleted(ctx, ev.SongID, filePath, relaxed)
}

// runLadder walks the catno → artist+title → title rungs, applying the
// quality gate at each. Returns the chosen peer+file, whether the relaxed
// gate produced the pick, and ok=false if every rung under both strict and
// relaxed gates failed.
//
// usedRelax: true iff the pick came from the relaxed-mode pass (strict
// found nothing across every rung). onRequestDownload uses this to decide
// whether to set LoadedSong.Relaxed — which it only does for user-
// initiated search (ROADMAP §v0.4 item 6).
//
// Laddering is the default; if cfg.LadderEnabled=false the function runs
// a single search with the plain "artist title" query and the strict gate
// — equivalent to pre-v0.4 behavior modulo the new thresholds.
func (s *Service) runLadder(ctx context.Context, ev bus.RequestDownload) (string, soulseek.SearchResult, bool, bool) {
	if !s.cfg.LadderEnabled {
		peer, file, ok := s.runSingleRung(ctx, ev, "artist_title", ev.Artist+" "+ev.Title, searchWindow, s.cfg.Gate, GateModeStrict)
		return peer, file, false, ok
	}

	rungs := buildLadder(ev)

	// Strict pass. Exit at the first rung whose filtered count ≥ LadderEnough.
	var bestPeer string
	var bestFile soulseek.SearchResult
	var bestCount int
	var haveAny bool

	for i, rung := range rungs {
		window := searchWindow
		if i > 0 && s.cfg.LadderRungWait > 0 {
			window = s.cfg.LadderRungWait
		}
		peer, file, count, ok := s.runRung(ctx, ev, i, rung.name, rung.query, window, s.cfg.Gate, GateModeStrict)
		if ok && count >= s.cfg.LadderEnough {
			return peer, file, false, true
		}
		// Carry the best-effort candidate across rungs so we still have
		// something if strict keeps rejecting but finds a few passes.
		if ok && count > bestCount {
			bestPeer, bestFile, bestCount, haveAny = peer, file, count, true
		}
	}

	// If strict produced anything at all (below LadderEnough), use it.
	if haveAny {
		return bestPeer, bestFile, false, true
	}

	// Relax pass over the same rungs with halved thresholds. Passive refill
	// doesn't surface the relaxation (silent); user-initiated search does
	// (onRequestDownload turns usedRelax=true into LoadedSong.Relaxed=true
	// iff ev.Strategy == StrategySearch). See ROADMAP §v0.4 item 6.
	relaxed := s.cfg.Gate.Relax()
	for i, rung := range rungs {
		window := searchWindow
		if i > 0 && s.cfg.LadderRungWait > 0 {
			window = s.cfg.LadderRungWait
		}
		peer, file, _, ok := s.runRung(ctx, ev, i, rung.name, rung.query, window, relaxed, GateModeRelaxed)
		if ok {
			return peer, file, true, true
		}
	}
	return "", soulseek.SearchResult{}, false, false
}

// rungSpec is one ladder step's name (for logging) and query.
type rungSpec struct {
	name  string // "catno" | "artist_title" | "title"
	query string
}

// buildLadder returns the three rungs in priority order. Rungs collapse
// when their source fields are empty:
//   - catno rung drops if CatalogNumber is empty
//   - artist_title rung drops if Artist is empty
//   - title rung drops if Title is empty (paranoia — every seeder sets it)
//
// The query string is fed verbatim to soulseek.Client.Search. Whitespace
// is normalized but no further cleaning is applied — Soulseek tolerates
// special characters better than any aggressive sanitizer.
func buildLadder(ev bus.RequestDownload) []rungSpec {
	var rungs []rungSpec
	if cn := strings.TrimSpace(ev.CatalogNumber); cn != "" {
		rungs = append(rungs, rungSpec{name: "catno", query: cn})
	}
	if ev.Artist != "" && ev.Title != "" {
		rungs = append(rungs, rungSpec{
			name:  "artist_title",
			query: strings.TrimSpace(ev.Artist + " " + ev.Title),
		})
	}
	if t := strings.TrimSpace(ev.Title); t != "" {
		rungs = append(rungs, rungSpec{name: "title", query: t})
	}
	return rungs
}

// runRung performs one Soulseek search + gate pass with full observability.
// Returns (peer, file, passCount, ok). ok=true means at least one result
// passed the gate; passCount is the total pass count at this rung.
//
// Writes to discovery_log at the StageLadder level (raw count) and at
// StageGate level (pass/fail totals). When a peer is picked, callers log
// StagePicked themselves after the broader ladder decides this rung wins.
func (s *Service) runRung(
	ctx context.Context,
	ev bus.RequestDownload,
	rungIndex int,
	rungName string,
	query string,
	window time.Duration,
	g GateConfig,
	mode GateMode,
) (string, soulseek.SearchResult, int, bool) {
	results, err := s.soulseek.Search(ctx, query, window)
	if err != nil {
		s.log.Warn("soulseek search failed; continuing ladder",
			"song_id", ev.SongID, "rung", rungName, "err", err)
		s.logw.Record(ctx, discovery.Record{
			SongID:      ev.SongID,
			Source:      discovery.SourceDownload,
			Stage:       discovery.StageLadder,
			Rung:        rungIndex,
			Query:       query,
			Outcome:     discovery.OutcomeError,
			Reason:      err.Error(),
			ResultCount: 0,
		})
		return "", soulseek.SearchResult{}, 0, false
	}

	s.logw.Record(ctx, discovery.Record{
		SongID:      ev.SongID,
		Source:      discovery.SourceDownload,
		Stage:       discovery.StageLadder,
		Rung:        rungIndex,
		Query:       query,
		Outcome:     ternaryOutcome(len(results) > 0, discovery.OutcomeOK, discovery.OutcomeNoResults),
		ResultCount: len(results),
	})

	passed, verdicts := filterGate(results, g)

	// Per-candidate gate logging. ROADMAP §v0.4 item 3 requires that "every
	// candidate that passes or fails the gate is logged with reason." One
	// StageGate row per SearchResult, carrying the per-file detail that
	// makes forensic "why did bitrate X get rejected" queries answerable.
	//
	// Pass rows: Outcome=ok (or Relaxed when mode=GateModeRelaxed), empty
	// Reason. Reject rows: Outcome=rejected_strict (or relaxed), Reason
	// carries classify()'s message, e.g. "bitrate 96 < 192".
	//
	// Volume: ~3 rungs × ~100 results = ~300 rows per song acquired. SQLite
	// handles this fine at Pi scale; the inserts run on the shared single
	// writer and are microseconds each.
	for _, v := range verdicts {
		s.logw.Record(ctx, discovery.Record{
			SongID:   ev.SongID,
			Source:   discovery.SourceDownload,
			Stage:    discovery.StageGate,
			Rung:     rungIndex,
			Query:    query,
			Outcome:  gateOutcomeFor(v.Pass, mode),
			Reason:   v.Reason,
			Filename: v.Result.Filename,
			Peer:     v.Result.Peer,
			Bitrate:  v.Result.Bitrate,
			Size:     v.Result.Size,
		})
	}

	peer, file, ok := pickFromPassed(passed)
	return peer, file, len(passed), ok
}

// gateOutcomeFor maps (verdict, mode) to the discovery_log Outcome string.
// A passed result under strict mode is "ok"; passed under relaxed mode is
// "relaxed" so aggregations can count how often relax actually saved a song.
// Failed results carry "rejected_strict" or "relaxed" depending on which
// pass produced them.
func gateOutcomeFor(pass bool, mode GateMode) discovery.Outcome {
	switch {
	case pass && mode == GateModeRelaxed:
		return discovery.OutcomeRelaxed
	case pass:
		return discovery.OutcomeOK
	case mode == GateModeRelaxed:
		return discovery.OutcomeRelaxed
	default:
		return discovery.OutcomeRejectedStrict
	}
}

// runSingleRung is the fallback used when LadderEnabled=false. Same
// contract as runRung but records only a single ladder/gate pair at
// rung 1 ("artist_title"), to keep the observability shape uniform
// regardless of whether the ladder is on.
func (s *Service) runSingleRung(
	ctx context.Context,
	ev bus.RequestDownload,
	rungName, query string,
	window time.Duration,
	g GateConfig,
	mode GateMode,
) (string, soulseek.SearchResult, bool) {
	peer, file, _, ok := s.runRung(ctx, ev, 1, rungName, query, window, g, mode)
	return peer, file, ok
}

// ternaryOutcome keeps the inline conditional in runRung short.
func ternaryOutcome(cond bool, ifTrue, ifFalse discovery.Outcome) discovery.Outcome {
	if cond {
		return ifTrue
	}
	return ifFalse
}

// recordFailed writes a terminal StageFailed row for post-mortem.
func (s *Service) recordFailed(ctx context.Context, ev bus.RequestDownload, reason string) {
	s.logw.Record(ctx, discovery.Record{
		SongID:  ev.SongID,
		Source:  discovery.SourceDownload,
		Stage:   discovery.StageFailed,
		Outcome: discovery.OutcomeError,
		Reason:  reason,
	})
}

// waitForDownload polls DownloadStatus until the transfer reaches a terminal
// state or downloadMaxTimeout elapses.
func (s *Service) waitForDownload(ctx context.Context, h soulseek.DownloadHandle) (string, error) {
	deadline := time.Now().Add(downloadMaxTimeout)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		state, err := s.soulseek.DownloadStatus(ctx, h)
		if err != nil {
			return "", err
		}
		switch state.State {
		case soulseek.DownloadCompleted:
			return state.FilePath, nil
		case soulseek.DownloadFailed:
			return "", errors.New("download: transfer failed")
		}
		if time.Now().After(deadline) {
			return "", errors.New("download: transfer timeout")
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

// ---- outbox emissions ----

func (s *Service) emitCompleted(ctx context.Context, songID uuid.UUID, filePath string, relaxed bool) error {
	return s.emit(ctx, bus.LoadedSong{
		SongID:   songID,
		FilePath: filePath,
		Status:   bus.LoadedStatusCompleted,
		Relaxed:  relaxed,
	})
}

func (s *Service) emitError(ctx context.Context, songID uuid.UUID) error {
	return s.emit(ctx, bus.LoadedSong{
		SongID: songID,
		Status: bus.LoadedStatusError,
	})
}

func (s *Service) emit(ctx context.Context, ev bus.LoadedSong) error {
	err := db.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		return bus.InsertOutboxTx(ctx, tx, bus.TypeLoadedSong, ev)
	})
	if err != nil {
		return fmt.Errorf("emit LoadedSong: %w", err)
	}
	if s.dispatcher != nil {
		s.dispatcher.Wake()
	}
	return nil
}
