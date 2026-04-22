package plugin

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/macabc/muzika/internal/similarity"
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

// TestPluginBucket_CandidatesRoundTrip exercises the candidates
// RPC end-to-end: muzika sends the seed, the plugin echoes back
// a hand-crafted candidate list, the bucket decodes + translates
// to similarity.Candidate values. This is the v0.6 PR B
// acceptance — without it we have no evidence that the wire
// protocol as published actually works.
func TestPluginBucket_CandidatesRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-based fake plugin is POSIX only")
	}
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "candidates-plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Plugin script: reply to hello with id=1 metadata, then to
	// candidates (id=2) with two canned candidates. Uses jq-free
	// string templating — the id field is the only thing we have
	// to match, and we know it's sequential from the manager's
	// spawn sequence.
	script := `#!/bin/sh
# hello
read -r _
printf '{"jsonrpc":"2.0","id":1,"result":{"id":"test.candidates","label":"Test","description":"","default_weight":4.0}}\n'
# candidates
read -r _
printf '{"jsonrpc":"2.0","id":2,"result":{"candidates":[{"title":"Homework","artist":"Daft Punk","confidence":0.9},{"title":"Discovery","artist":"Daft Punk"}]}}\n'
# wait for shutdown
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
		t.Fatalf("expected 1 bucket, got %d", len(bs))
	}
	cands, err := bs[0].Candidates(ctx, dummySeed())
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(cands) != 2 {
		t.Fatalf("got %d candidates, want 2", len(cands))
	}
	if cands[0].Title != "Homework" || cands[0].Artist != "Daft Punk" || cands[0].Confidence != 0.9 {
		t.Errorf("first candidate wrong: %+v", cands[0])
	}
	if cands[1].Title != "Discovery" || cands[1].Confidence != 0 {
		// Confidence 0 is the "unset" sentinel the engine's
		// candidateConfidence helper later clamps to 1.0 — bucket
		// layer passes through zero unchanged.
		t.Errorf("second candidate wrong: %+v", cands[1])
	}
}

// TestPluginBucket_RespawnsOnCrash is the heavy-lifting test
// for the v0.6 PR B supervisor. Plugin exits after hello; the
// supervisor detects it via WaitClosed and respawns. The second
// child serves the candidates call that would have timed out
// against the first. Uses a tmpfile as a sentinel so we can
// tell which generation of the process is alive.
func TestPluginBucket_RespawnsOnCrash(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-based fake plugin is POSIX only")
	}
	// Override the respawn backoffs to make the test fast.
	origBackoffs := respawnBackoffs
	t.Cleanup(func() { respawnBackoffs = origBackoffs })
	respawnBackoffs = []time.Duration{50 * time.Millisecond, 50 * time.Millisecond}

	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "crashy")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sentinel := filepath.Join(dir, "gen.count")
	// The script writes its "generation" by incrementing a file.
	// First launch = gen 1 (exits after hello); second launch =
	// gen >= 2, serves candidates. Proves the supervisor actually
	// re-forked the script rather than the same process limping on.
	script := `#!/bin/sh
SENTINEL=` + sentinel + `
if [ -f "$SENTINEL" ]; then
  count=$(cat "$SENTINEL")
else
  count=0
fi
gen=$((count + 1))
echo "$gen" > "$SENTINEL"
# Reply to hello whatever generation we are.
read -r _
printf '{"jsonrpc":"2.0","id":1,"result":{"id":"test.crashy","label":"Crashy","default_weight":3}}\n'
if [ "$gen" -eq 1 ]; then
  # First generation: exit immediately after hello, simulating crash.
  exit 0
fi
# Later generations: stay alive, serve candidates.
read -r _
printf '{"jsonrpc":"2.0","id":2,"result":{"candidates":[{"title":"Survived","artist":"gen'"$gen"'"}]}}\n'
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
		t.Fatalf("expected 1 bucket, got %d", len(bs))
	}

	// Poll Candidates until the supervisor has respawned + the
	// second-generation child answers. Up to 3s of retries;
	// backoff is 50ms so this finishes in a handful.
	deadline := time.Now().Add(3 * time.Second)
	var cands []similarity.Candidate
	for time.Now().Before(deadline) {
		got, _ := bs[0].Candidates(ctx, dummySeed())
		if len(got) > 0 {
			cands = got
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(cands) == 0 {
		b, _ := os.ReadFile(sentinel)
		t.Fatalf("no candidates after respawn; gen file = %q", string(b))
	}
	if cands[0].Title != "Survived" {
		t.Errorf("unexpected candidate: %+v", cands[0])
	}
	// Sentinel should show gen >= 2 (first crashed, second served).
	genRaw, _ := os.ReadFile(sentinel)
	if strings.TrimSpace(string(genRaw)) == "1" {
		t.Errorf("respawn didn't happen; gen file = %q", genRaw)
	}
}

// dummySeed is a shared minimal seed fixture for the candidates
// tests — enough to not trip the Title/Artist empty checks in
// the bucket adapter.
func dummySeed() similarity.Seed {
	return similarity.Seed{Title: "Seed", Artist: "Tester"}
}
