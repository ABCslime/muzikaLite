package plugin

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/macabc/muzika/internal/similarity"
)

// ExecutableName is the convention muzika looks for in each
// plugin directory. Kept stable as a constant so plugin
// authors can depend on it without reading this file.
const ExecutableName = "bucket"

// respawnBackoffs is the schedule the supervisor uses when a
// plugin crashes. Each entry is the wait before the n-th respawn
// attempt. A successful hello + first candidates reply resets
// the counter; maxCrashes consecutive failures without success
// mark the plugin permanently dead (until muzika restarts).
var respawnBackoffs = []time.Duration{
	1 * time.Second,
	5 * time.Second,
	30 * time.Second,
	5 * time.Minute,
}

// maxCrashes is the consecutive-failure cap. A plugin that
// crashes 5 times in a row without a successful respawn is
// considered broken by design — respawning forever would hide
// the bug from the operator and burn CPU.
const maxCrashes = 5

// Manager owns the lifecycle of every spawned plugin bucket:
// filesystem discovery at startup, child-process cleanup at
// shutdown, and the mapping from plugin dir → registered
// similarity.Bucket. All plugins it discovers are returned
// as Bucket values that main.go registers on
// similarity.Service alongside the built-in ones.
//
// Zero background goroutines beyond those owned by each
// individual Process (one readLoop per plugin). Manager
// itself is passive: Load happens once at startup, Close
// at shutdown.
type Manager struct {
	log    *slog.Logger
	muzika string // muzika version advertised on hello

	mu      sync.Mutex
	procs   []*Process          // current set (possibly swapped by supervisor)
	buckets []similarity.Bucket // parallel to procs, same length

	// v0.6 PR B: supervisor infrastructure. ctx is cancelled on
	// Close to break every supervise goroutine out of its
	// WaitClosed wait; wg tracks them so Close can block until
	// they've all finished and it's safe to return.
	superCtx    context.Context
	superCancel context.CancelFunc
	wg          sync.WaitGroup
}

// NewManager constructs a passive Manager. Load populates the
// bucket list; Close tears down spawned children.
func NewManager(muzikaVersion string, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		log:         log.With("mod", "similarity/plugin"),
		muzika:      muzikaVersion,
		superCtx:    ctx,
		superCancel: cancel,
	}
}

// Load scans dir for subdirectories containing an executable
// named "bucket", spawns each as a child process, runs the
// hello handshake, and registers the plugin as a Bucket.
//
// Errors per plugin are logged and skipped: one broken plugin
// doesn't stop the rest from loading. Returns only non-plugin-
// specific errors (e.g. dir doesn't exist + can't be created).
//
// A non-existent dir is not an error: plugins are optional.
// The return value is zero buckets in that case. Callers that
// want plugins to be required should check len(Buckets()) > 0
// themselves.
//
// Safe to call once at startup. Concurrent calls are not
// supported (there's no reason to reload).
func (m *Manager) Load(ctx context.Context, dir string) error {
	if dir == "" {
		m.log.Debug("plugin loader: no dir configured; skipping")
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			m.log.Debug("plugin loader: dir does not exist; no plugins",
				"dir", dir)
			return nil
		}
		return fmt.Errorf("plugin loader: read %s: %w", dir, err)
	}
	// Deterministic order so tests can reason about registration
	// sequence + the Settings UI shows plugins in a predictable
	// order across restarts.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pluginDir := filepath.Join(dir, e.Name())
		exe := filepath.Join(pluginDir, ExecutableName)
		if _, err := os.Stat(exe); err != nil {
			m.log.Debug("plugin loader: skipping (no bucket executable)",
				"plugin", e.Name(), "exe", exe)
			continue
		}
		m.loadOne(ctx, pluginDir, e.Name())
	}
	m.log.Info("plugin loader: finished scan",
		"dir", dir, "loaded", len(m.buckets))
	return nil
}

// loadOne spawns one plugin, runs hello, registers it, and
// starts the supervisor goroutine that will respawn the plugin
// on crash. Errors on the initial load are logged and the plugin
// is skipped.
func (m *Manager) loadOne(ctx context.Context, pluginDir, name string) {
	proc, meta, err := m.spawnAndHello(ctx, pluginDir, name)
	if err != nil {
		m.log.Warn("plugin loader: initial spawn/hello failed; skipping",
			"plugin", name, "err", err)
		return
	}
	bucket := newPluginBucket(proc, meta)

	// Accept this plugin. Track the process for Close; add a
	// wrapper bucket for the engine.
	m.mu.Lock()
	m.procs = append(m.procs, proc)
	m.buckets = append(m.buckets, bucket)
	m.mu.Unlock()
	m.log.Info("plugin loader: registered",
		"plugin", name, "bucket_id", meta.ID,
		"default_weight", meta.DefaultWeight)

	// Supervisor goroutine: watches for crashes, respawns with
	// exponential backoff, swaps bucket.proc atomically so the
	// engine sees the new child without re-registering.
	m.wg.Add(1)
	go m.supervise(pluginDir, name, bucket)
}

// spawnAndHello is the shared startup path used by loadOne and
// the supervisor's respawn loop: fork the child, run hello,
// return either (proc, meta) ready for use or a cleaned-up
// error. A failed hello closes the proc before returning so
// the caller doesn't leak a child.
func (m *Manager) spawnAndHello(ctx context.Context, pluginDir, name string) (*Process, HelloResult, error) {
	proc, err := spawnProcess(ctx, pluginDir, name, m.log)
	if err != nil {
		return nil, HelloResult{}, err
	}
	meta, err := proc.Hello(ctx, m.muzika)
	if err != nil {
		_ = proc.Close()
		return nil, HelloResult{}, err
	}
	return proc, meta, nil
}

// supervise blocks on the current proc's WaitClosed signal; when
// it fires, the supervisor checks whether shutdown is in
// progress (ctx cancelled). If not, it waits a backoff period
// and respawns. maxCrashes consecutive failures — either spawn
// error, hello failure, or fresh death before a successful
// hello — permanently mark the plugin dead.
//
// Runs exactly one goroutine per registered plugin for the
// lifetime of the Manager. Exits on ctx.Done() (Close) or after
// maxCrashes.
func (m *Manager) supervise(pluginDir, name string, bucket *pluginBucket) {
	defer m.wg.Done()
	ctx := m.superCtx
	crashes := 0
	for {
		proc := bucket.currentProc()
		if proc == nil {
			return
		}
		// Wait for the child to die, either via Close (shutdown)
		// or natural exit (crash).
		select {
		case <-proc.WaitClosed():
		case <-ctx.Done():
			return
		}
		// If ctx is cancelled, the Close() path is handling teardown.
		// Exit without attempting a respawn.
		select {
		case <-ctx.Done():
			return
		default:
		}

		crashes++
		if crashes > maxCrashes {
			m.log.Warn("plugin supervisor: giving up after repeated crashes",
				"plugin", name, "crashes", crashes-1)
			bucket.setProc(nil)
			return
		}
		wait := respawnBackoffs[min(crashes-1, len(respawnBackoffs)-1)]
		m.log.Warn("plugin supervisor: crashed, scheduling respawn",
			"plugin", name, "crashes", crashes, "wait", wait)

		// Sleep for backoff, but allow Close to preempt.
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return
		}

		newProc, meta, err := m.spawnAndHello(ctx, pluginDir, name)
		if err != nil {
			m.log.Warn("plugin supervisor: respawn failed",
				"plugin", name, "err", err)
			// Don't reset crashes — another failed attempt.
			// Keep the bucket's proc nil so Candidates returns empty.
			bucket.setProc(nil)
			continue
		}
		// Respawn succeeded: swap in the new proc. Metadata is
		// cached on bucket at creation time; if a plugin version
		// skew changed its id between spawns we don't currently
		// re-register — keep a stable ID across respawns is part
		// of the plugin author contract, documented in PR C's
		// README.
		_ = meta
		bucket.setProc(newProc)
		// Track the new proc for Close (so shutdown kills it too).
		m.mu.Lock()
		m.procs = append(m.procs, newProc)
		m.mu.Unlock()
		crashes = 0
		m.log.Info("plugin supervisor: respawned",
			"plugin", name)
	}
}


// Buckets returns a snapshot of the registered plugin Buckets.
// Safe to call concurrently with Close; returns the live list
// if still loading, an empty slice after Close.
func (m *Manager) Buckets() []similarity.Bucket {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]similarity.Bucket, len(m.buckets))
	copy(out, m.buckets)
	return out
}

// Close terminates every spawned plugin and stops every
// supervisor goroutine. Called from main.go's shutdown sequence
// alongside the bus/outbox teardown. Idempotent — Process.Close
// and superCancel are both idempotent.
//
// Order matters: cancel supervisor ctx first so supervisors
// don't race to respawn an already-killed process; then close
// each proc (which signals WaitClosed); finally wait for
// supervisors to exit.
func (m *Manager) Close() error {
	m.superCancel()
	m.mu.Lock()
	procs := m.procs
	m.procs = nil
	m.buckets = nil
	m.mu.Unlock()
	for _, p := range procs {
		_ = p.Close()
	}
	m.wg.Wait()
	return nil
}
