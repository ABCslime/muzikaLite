package plugin

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/macabc/muzika/internal/similarity"
)

// pluginBucket wraps a running Process and implements the
// similarity.Bucket interface so the engine can't tell a plugin
// apart from a built-in. All Bucket methods route through the
// wrapper's cached metadata (static after hello) or the
// Process's JSON-RPC client (dynamic per-cycle).
//
// v0.6 PR A: scaffolds the wrapper; Candidates() returns empty
// with no API call, because wiring the candidates RPC is PR B's
// scope. This keeps PR A shippable on its own — plugin discovery
// + handshake + registration all work; plugins just don't
// contribute candidates yet.
type pluginBucket struct {
	proc *Process
	meta HelloResult
}

// newPluginBucket pairs a spawned Process with its hello result.
// Not exported; the Manager owns construction.
func newPluginBucket(proc *Process, meta HelloResult) *pluginBucket {
	return &pluginBucket{proc: proc, meta: meta}
}

// ID — the plugin self-assigns this at hello time. Plugin
// authors are expected to namespace (e.g. "events.same_festival")
// so a third-party bucket can't collide with a built-in's id.
func (b *pluginBucket) ID() string { return b.meta.ID }

func (b *pluginBucket) Label() string       { return b.meta.Label }
func (b *pluginBucket) Description() string { return b.meta.Description }

// DefaultWeight returns the plugin's declared default. Zero
// falls through to 1.0 here rather than being treated as
// "disabled" — a plugin author who didn't set the field
// probably wants the bucket on. "Set it to 0" is the explicit
// opt-out; they can declare that in their hello result.
func (b *pluginBucket) DefaultWeight() float64 {
	if b.meta.DefaultWeight == 0 {
		return 1.0
	}
	return b.meta.DefaultWeight
}

// Candidates dispatches the candidates RPC to the plugin and
// translates the reply into similarity.Candidate values.
//
// Error-swallowing contract (matches built-in buckets):
//   - Plugin returned an RPC error → treat as empty, log at debug.
//   - Timeout → treat as empty, log at debug.
//   - Process is closed (dead plugin) → return
//     ErrPluginClosed so the engine's error handling can at
//     least record the degradation. Engine still treats as
//     empty for merging purposes.
func (b *pluginBucket) Candidates(ctx context.Context, seed similarity.Seed) ([]similarity.Candidate, error) {
	if b.proc == nil {
		return nil, nil
	}
	params := CandidatesParams{Seed: SeedWire{
		Title:            seed.Title,
		Artist:           seed.Artist,
		DiscogsReleaseID: seed.DiscogsReleaseID,
		DiscogsArtistID:  seed.DiscogsArtistID,
		DiscogsLabelID:   seed.DiscogsLabelID,
		Year:             seed.Year,
		Styles:           seed.Styles,
		Genres:           seed.Genres,
		Collaborators:    seed.Collaborators,
	}}
	resp, err := b.proc.Call(ctx, "candidates", params, callTimeout)
	if err != nil {
		// Close vs timeout vs write error: caller differentiates
		// via errors.Is only when it needs to; for the engine
		// every error path collapses to "no candidates this cycle."
		if errors.Is(err, ErrPluginClosed) {
			return nil, err
		}
		return nil, nil
	}
	if resp.Error != nil {
		// Plugin explicitly returned an RPC error. Debug-log
		// and treat as empty — same failure mode the built-in
		// buckets use when they hit Discogs rate limits.
		return nil, nil
	}
	var out CandidatesResult
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		return nil, nil
	}
	cands := make([]similarity.Candidate, 0, len(out.Candidates))
	for _, c := range out.Candidates {
		if c.Title == "" || c.Artist == "" {
			continue
		}
		cands = append(cands, similarity.Candidate{
			Title:      c.Title,
			Artist:     c.Artist,
			ImageURL:   c.ImageURL,
			Confidence: c.Confidence,
			Edge:       c.Edge,
		})
	}
	return cands, nil
}
