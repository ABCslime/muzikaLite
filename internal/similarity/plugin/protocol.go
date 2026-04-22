// Package plugin implements the v0.6 local-plugin-bucket
// mechanism: muzika scans a filesystem directory at startup,
// spawns each plugin as a long-lived child process, and wraps
// it behind the similarity.Bucket interface. The engine treats
// a plugin bucket identically to a built-in one.
//
// Protocol is JSON-RPC 2.0 style over the child's stdio:
// newline-delimited JSON messages, request/response keyed by
// numeric id. Two methods in the v0.6 protocol:
//
//   hello     — one-shot at spawn time; plugin returns its
//               static metadata (id, label, description,
//               defaultWeight). If the plugin fails this
//               handshake it's marked dead and not registered.
//
//   candidates — called on each refill cycle with the current
//               seed; plugin returns a candidate list the
//               engine merges with built-in bucket output.
//
// Wire stability: PR A publishes v1. Field additions are safe
// on both sides (JSON tolerates unknown keys). Removing a field
// or changing its semantics is a breaking change — bump the
// protocol version constant and document the migration in the
// plugin-author README.
package plugin

import "encoding/json"

// ProtocolVersion is the wire version advertised in the hello
// request's params. Plugin authors can branch on this if their
// code needs to support multiple muzika versions.
const ProtocolVersion = "1"

// Request is one JSON-RPC request message written to the
// plugin's stdin. id is a monotonic counter per process so
// multiple in-flight requests match to their responses.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      uint64          `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is the plugin's reply. Either Result or Error is
// populated; callers treat a well-formed error as "bucket had
// nothing for this cycle" rather than a fatal protocol breach.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      uint64          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError mirrors the JSON-RPC 2.0 error object. Code values
// are plugin-defined; muzika doesn't interpret them.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// HelloParams is the payload muzika sends on the hello call.
// Kept open-shaped (just a version) so future muzika features
// can advertise themselves without forcing a protocol bump.
type HelloParams struct {
	MuzikaVersion   string `json:"muzika_version"`
	ProtocolVersion string `json:"protocol_version"`
}

// HelloResult is what the plugin returns: the static bucket
// metadata muzika uses to register it. id + label are required;
// description + defaultWeight are optional (defaulted muzika-
// side if zero/empty — but plugin authors SHOULD supply them
// for the Settings UI).
type HelloResult struct {
	ID            string  `json:"id"`
	Label         string  `json:"label"`
	Description   string  `json:"description,omitempty"`
	DefaultWeight float64 `json:"default_weight,omitempty"`
}

// SeedWire is the seed payload sent to the plugin on the
// candidates call. Mirrors similarity.Seed minus the UUID
// fields that don't cross the process boundary meaningfully
// (UserID, SongID) — plugins work against the Discogs-derived
// features and the (title, artist) pair. Any field may be
// zero-valued; plugin authors bail out gracefully on what
// they need.
type SeedWire struct {
	Title            string   `json:"title,omitempty"`
	Artist           string   `json:"artist,omitempty"`
	DiscogsReleaseID int      `json:"discogs_release_id,omitempty"`
	DiscogsArtistID  int      `json:"discogs_artist_id,omitempty"`
	DiscogsLabelID   int      `json:"discogs_label_id,omitempty"`
	Year             int      `json:"year,omitempty"`
	Styles           []string `json:"styles,omitempty"`
	Genres           []string `json:"genres,omitempty"`
	Collaborators    []int    `json:"collaborators,omitempty"`
}

// CandidatesParams is the payload for the candidates call.
// Seed carries the hydrated features; muzika may add timeout
// hints or user identifiers in future protocol versions.
type CandidatesParams struct {
	Seed SeedWire `json:"seed"`
}

// CandidateWire is one row the plugin emits. title + artist
// are required; confidence defaults to 1.0 when unset (same
// convention as the built-in buckets). imageURL + edge are
// optional and feed into the v0.7 graph view unchanged.
type CandidateWire struct {
	Title      string         `json:"title"`
	Artist     string         `json:"artist"`
	Confidence float64        `json:"confidence,omitempty"`
	ImageURL   string         `json:"image_url,omitempty"`
	Edge       map[string]any `json:"edge,omitempty"`
}

// CandidatesResult wraps the emitted candidate slice. An empty
// slice is a valid "nothing for this seed" response and not an
// error — same contract as built-in Bucket.Candidates.
type CandidatesResult struct {
	Candidates []CandidateWire `json:"candidates"`
}
