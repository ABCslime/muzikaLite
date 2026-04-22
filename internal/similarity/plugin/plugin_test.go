package plugin

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestManager_LoadsFakePlugin spawns a shell-script "plugin" that
// speaks the wire protocol well enough to complete the hello
// handshake. Tests the Manager's discovery + process spawn + hello
// path end-to-end without needing a real compiled plugin. Skipped
// on Windows because the shell-script approach is POSIX only.
func TestManager_LoadsFakePlugin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-based fake plugin is POSIX only")
	}

	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "hello-only")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// A minimal plugin that responds to hello with fixed metadata
	// then exits cleanly when stdin closes. Enough to prove the
	// full spawn + handshake path works against a real OS process.
	//
	// We hard-code the id: we don't parse the request, we just
	// emit a response with id=1 which matches the first (only)
	// request the Manager sends during load.
	script := `#!/bin/sh
# drain stdin once, reply, wait for EOF.
read -r line
printf '{"jsonrpc":"2.0","id":1,"result":{"id":"test.hello_only","label":"Hello only","description":"test","default_weight":2.5}}\n'
# Wait for stdin EOF to exit cleanly (Manager.Close triggers this).
while read -r _; do :; done
`
	if err := os.WriteFile(filepath.Join(pluginDir, ExecutableName), []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	m := NewManager("v0.6-test", slog.New(slog.NewTextHandler(os.Stderr, nil)))
	t.Cleanup(func() { _ = m.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.Load(ctx, dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	bs := m.Buckets()
	if len(bs) != 1 {
		t.Fatalf("got %d buckets, want 1", len(bs))
	}
	if got := bs[0].ID(); got != "test.hello_only" {
		t.Errorf("ID = %q, want test.hello_only", got)
	}
	if got := bs[0].Label(); got != "Hello only" {
		t.Errorf("Label = %q, want 'Hello only'", got)
	}
	if got := bs[0].DefaultWeight(); got != 2.5 {
		t.Errorf("DefaultWeight = %v, want 2.5", got)
	}
}

// TestManager_SkipsPluginWithoutExecutable: a plugin directory
// that doesn't contain the "bucket" executable is ignored rather
// than failing the whole scan. Ops can leave README files, in-
// progress installs, etc. in the plugin tree without breaking
// startup.
func TestManager_SkipsPluginWithoutExecutable(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "empty-dir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty-dir", "README.md"), []byte("TODO"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}

	m := NewManager("v0.6-test", slog.New(slog.NewTextHandler(os.Stderr, nil)))
	t.Cleanup(func() { _ = m.Close() })
	if err := m.Load(context.Background(), dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := len(m.Buckets()); got != 0 {
		t.Errorf("got %d buckets, want 0", got)
	}
}

// TestManager_MissingDirIsNotAnError: MUZIKA_BUCKET_PLUGIN_DIR
// pointed at a nonexistent path means "no plugins," not "fail
// startup." Matches the behavior of the empty-string config case.
func TestManager_MissingDirIsNotAnError(t *testing.T) {
	m := NewManager("v0.6-test", slog.New(slog.NewTextHandler(os.Stderr, nil)))
	t.Cleanup(func() { _ = m.Close() })
	if err := m.Load(context.Background(), "/does/not/exist"); err != nil {
		t.Errorf("got err %v, want nil", err)
	}
	if got := len(m.Buckets()); got != 0 {
		t.Errorf("got %d buckets, want 0", got)
	}
}

// TestManager_EmptyDirConfigIsNoop: the empty-string config is
// the "plugins disabled" sentinel. Must not touch the filesystem.
func TestManager_EmptyDirConfigIsNoop(t *testing.T) {
	m := NewManager("v0.6-test", slog.New(slog.NewTextHandler(os.Stderr, nil)))
	t.Cleanup(func() { _ = m.Close() })
	if err := m.Load(context.Background(), ""); err != nil {
		t.Errorf("empty dir: got err %v, want nil", err)
	}
	if got := len(m.Buckets()); got != 0 {
		t.Errorf("got %d buckets, want 0", got)
	}
}
