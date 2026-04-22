package plugin_test

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/macabc/muzika/internal/similarity"
	"github.com/macabc/muzika/internal/similarity/plugin"
)

// _, thisFile = runtime.Caller(0) at package-init time would
// be cleaner, but Go won't let us init a var from runtime.Caller
// at package scope without an init func. Helper closure instead.
func thisTestFile() string {
	_, f, _, _ := runtime.Caller(0)
	return f
}

// TestReferencePlugin_EndToEnd builds the tools/bucket-example
// reference plugin, drops the binary into a test plugin dir,
// and runs it through the real Manager. If this test fails the
// v0.6 contract has drifted between the plugin template and
// muzika's loader — plugin authors copying the reference would
// hit the same failure. Fixing either side is the diff.
//
// Skipped on Windows (shell-independent, but the build cmd
// invocation is simpler to maintain POSIX-only for now) and on
// tests that don't have the Go toolchain accessible.
func TestReferencePlugin_EndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("reference plugin build on Windows not covered")
	}
	if testing.Short() {
		t.Skip("skipping: -short; reference-plugin build is ~600ms")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not in PATH")
	}

	// tempDir is our MUZIKA_BUCKET_PLUGIN_DIR equivalent for the
	// test. The Manager expects one subdir per plugin; we create
	// "example/" inside it.
	tempDir := t.TempDir()
	pluginDir := filepath.Join(tempDir, "example")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("mkdir plugin dir: %v", err)
	}
	binPath := filepath.Join(pluginDir, plugin.ExecutableName)

	// Resolve the module root regardless of where `go test` is
	// invoked from. internal/similarity/plugin is three levels
	// below module root; repo layout has been stable since v0.1.
	moduleRoot := filepath.Join(filepath.Dir(thisTestFile()), "..", "..", "..")
	pkgPath := filepath.Join(moduleRoot, "tools", "bucket-example")

	buildCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(buildCtx, "go", "build", "-o", binPath, ".")
	cmd.Dir = pkgPath
	cmd.Stderr = os.Stderr
	if out, err := cmd.Output(); err != nil {
		t.Fatalf("go build bucket-example: %v (stdout=%q)", err, string(out))
	}

	m := plugin.NewManager("v0.6-test", slog.New(slog.NewTextHandler(os.Stderr, nil)))
	t.Cleanup(func() { _ = m.Close() })

	ctx, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	if err := m.Load(ctx, tempDir); err != nil {
		t.Fatalf("Manager.Load: %v", err)
	}

	bs := m.Buckets()
	if len(bs) != 1 {
		t.Fatalf("got %d buckets, want 1", len(bs))
	}
	b := bs[0]
	// Hello assertions: IDs and labels in the reference must
	// match what the plugin author README promises.
	if b.ID() != "example.echo_artist" {
		t.Errorf("ID = %q, want example.echo_artist", b.ID())
	}
	if b.DefaultWeight() != 1.0 {
		t.Errorf("DefaultWeight = %v, want 1.0", b.DefaultWeight())
	}

	// Candidates round-trip: seed's artist must appear on every
	// returned candidate (the plugin's echo contract).
	cands, err := b.Candidates(ctx, similarity.Seed{
		Title:  "Some Title",
		Artist: "TestArtist",
	})
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(cands) != 3 {
		t.Fatalf("got %d candidates, want 3 (reference plugin fixture)", len(cands))
	}
	for i, c := range cands {
		if c.Artist != "TestArtist" {
			t.Errorf("candidate %d artist = %q, want TestArtist (echo broke)", i, c.Artist)
		}
	}
}

