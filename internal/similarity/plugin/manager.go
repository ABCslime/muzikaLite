package plugin

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/macabc/muzika/internal/similarity"
)

// ExecutableName is the convention muzika looks for in each
// plugin directory. Kept stable as a constant so plugin
// authors can depend on it without reading this file.
const ExecutableName = "bucket"

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
	procs  []*Process // for Close
	muzika string     // muzika version advertised on hello

	mu      sync.Mutex
	buckets []similarity.Bucket
}

// NewManager constructs a passive Manager. Load populates the
// bucket list; Close tears down spawned children.
func NewManager(muzikaVersion string, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{
		log:    log.With("mod", "similarity/plugin"),
		muzika: muzikaVersion,
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

// loadOne spawns one plugin, runs hello, registers it.
// Errors are logged and the plugin is skipped — the caller
// doesn't need to know.
func (m *Manager) loadOne(ctx context.Context, pluginDir, name string) {
	proc, err := spawnProcess(ctx, pluginDir, name, m.log)
	if err != nil {
		m.log.Warn("plugin loader: spawn failed",
			"plugin", name, "err", err)
		return
	}
	meta, err := proc.Hello(ctx, m.muzika)
	if err != nil {
		m.log.Warn("plugin loader: hello failed; closing",
			"plugin", name, "err", err)
		_ = proc.Close()
		return
	}
	// Accept this plugin. Track the process for Close; add a
	// wrapper bucket for the engine.
	m.mu.Lock()
	m.procs = append(m.procs, proc)
	m.buckets = append(m.buckets, newPluginBucket(proc, meta))
	m.mu.Unlock()
	m.log.Info("plugin loader: registered",
		"plugin", name, "bucket_id", meta.ID,
		"default_weight", meta.DefaultWeight)
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

// Close terminates every spawned plugin. Called from main.go's
// shutdown sequence alongside the bus/outbox teardown.
// Idempotent — Process.Close is also idempotent.
func (m *Manager) Close() error {
	m.mu.Lock()
	procs := m.procs
	m.procs = nil
	m.buckets = nil
	m.mu.Unlock()
	for _, p := range procs {
		_ = p.Close()
	}
	return nil
}
