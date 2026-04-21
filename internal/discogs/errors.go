package discogs

import "errors"

// ErrNoResults is returned when the Discogs search returns zero usable items
// (either empty results, or every result had a malformed title we couldn't
// split into artist/title). Callers treat it like bandcamp.ErrNoResults:
// emit LoadedSong{Error} via the outbox so the stub gets reaped.
var ErrNoResults = errors.New("discogs: no results")

// ErrRateLimited is returned when the Discogs API responds 429. The
// per-client token bucket should prevent this, but we still map the
// upstream signal so the worker can distinguish it from other 5xx errors
// in the discovery_log. The stub is reaped either way; the refiller will
// re-observe the short queue on its next Trigger.
var ErrRateLimited = errors.New("discogs: rate limited")
