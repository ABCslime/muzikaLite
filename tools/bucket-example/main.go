// Command bucket-example is the reference implementation of a
// muzika v0.6 similarity bucket plugin. Speaks JSON-RPC 2.0
// over stdio per the spec in internal/similarity/plugin/protocol.go.
//
// This plugin is intentionally minimal: it returns a fixed set
// of candidates derived from the seed's artist (so tests can
// verify the echo) and demonstrates the protocol skeleton
// without a real data source behind it. Copy this file, change
// the identity in helloResult, and replace buildCandidates with
// real logic (web API call, local DB lookup, scraped festival
// lineups, whatever your bucket is) to ship a working plugin.
//
// Build + install:
//
//   go build -o ~/.muzika/buckets/example/bucket ./tools/bucket-example
//
// Run muzika with MUZIKA_BUCKET_PLUGIN_DIR=~/.muzika/buckets.
// muzika scans subdirectories, spawns each's "bucket" executable
// once at startup, and registers the result on the similarity
// engine alongside built-in buckets.
//
// Plugin lifecycle contract:
//
//   - muzika calls "hello" once; reply with your bucket metadata.
//   - muzika calls "candidates" on every similar-mode refill cycle
//     while your bucket's user weight is > 0.
//   - muzika closes your stdin at shutdown; exit cleanly when
//     you see EOF.
//   - If you panic or exit unexpectedly, muzika's supervisor
//     respawns you with exponential backoff (1s / 5s / 30s / 5m).
//     Five consecutive failures within that window mark you
//     dead until muzika restarts.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// Static metadata returned on the hello call. A real plugin
// changes these identifiers and picks a default_weight based on
// how high-signal its output is relative to the built-in buckets
// (weights 1-5 are the conventional range).
var helloResult = map[string]any{
	"id":             "example.echo_artist",
	"label":          "Example (echo artist)",
	"description":    "Reference plugin. Returns fake releases attributed to the seed's artist.",
	"default_weight": 1.0,
}

// rpcRequest / rpcResponse mirror the protocol types from
// internal/similarity/plugin/protocol.go. Kept local so this
// plugin has no muzika imports — by design, plugins ship as
// standalone binaries and the wire protocol is the only coupling.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      uint64          `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      uint64 `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// candidatesParams is the subset of the candidates RPC payload
// this plugin cares about. A real plugin might decode more of
// the Seed wire type — see SeedWire in muzika's protocol.go for
// the full shape.
type candidatesParams struct {
	Seed struct {
		Title  string `json:"title"`
		Artist string `json:"artist"`
	} `json:"seed"`
}

func main() {
	// Line-delimited JSON over stdin. A well-behaved plugin
	// processes one request at a time; concurrency comes from
	// the Go side (muzika's engine runs buckets in parallel
	// goroutines, but each bucket's RPC is serialized against
	// its own plugin).
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	for scanner.Scan() {
		var req rpcRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			// Ignore malformed lines rather than exit — the
			// supervisor would respawn us anyway, but a tolerant
			// plugin keeps a partial failure from becoming a
			// full outage.
			continue
		}
		resp := dispatch(req)
		if err := writeResponse(os.Stdout, resp); err != nil {
			// stdout write failure = muzika closed the pipe.
			// Exit cleanly; the supervisor decides whether to
			// respawn (it won't, if this was a normal shutdown).
			return
		}
	}
	// scanner.Err non-nil iff stdin had a real error; nil on EOF.
	// Either way we're done.
}

// dispatch routes one request to the matching method. Plugin
// authors typically add cases here as the protocol grows (v0.6
// ships hello + candidates; v0.7 may add more).
func dispatch(req rpcRequest) rpcResponse {
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "hello":
		resp.Result = helloResult
	case "candidates":
		resp.Result = handleCandidates(req.Params)
	default:
		resp.Error = &rpcError{
			Code:    -32601,
			Message: fmt.Sprintf("method not supported: %s", req.Method),
		}
	}
	return resp
}

// handleCandidates is where a real plugin does its work. This
// reference returns three deterministic candidates whose titles
// include the seed's artist — enough for an integration test to
// verify the echo made it all the way through the pipeline.
//
// A real bucket would consult whatever backend it cares about:
// a local SQLite catalog, a remote API, a scraped data file.
// The only constraints are the 2s timeout muzika enforces per
// call (return quickly or return empty) and the response shape
// (see CandidatesResult in muzika's protocol.go).
func handleCandidates(raw json.RawMessage) map[string]any {
	var p candidatesParams
	_ = json.Unmarshal(raw, &p) // tolerate malformed; emit empty
	artist := p.Seed.Artist
	if artist == "" {
		artist = "Unknown Artist"
	}
	return map[string]any{
		"candidates": []map[string]any{
			{"title": "Example Track 1", "artist": artist, "confidence": 0.8},
			{"title": "Example Track 2", "artist": artist, "confidence": 0.6},
			{"title": "Example Track 3", "artist": artist, "confidence": 0.4},
		},
	}
}

// writeResponse serializes one rpcResponse as a newline-
// delimited JSON object on w. Critical that every message ends
// with exactly one newline — muzika's scanner splits on lines.
func writeResponse(w io.Writer, resp rpcResponse) error {
	b, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}
