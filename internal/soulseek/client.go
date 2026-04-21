// Package soulseek defines the abstraction over the Soulseek network backend.
//
// Only one implementation ships: native.go, which wraps github.com/ABCslime/gosk
// (a pure-Go Soulseek client). The old HTTP-to-slskd-daemon backend was
// retired in v0.3.0 — muzika is now a single Go binary on the Pi with no
// sidecar. Keeping the Client interface around lets us swap in a different
// backend later without touching the worker code.
package soulseek

import (
	"context"
	"time"
)

// Client is the backend-agnostic surface. Keep it small on purpose.
// See ARCHITECTURE.md §7 for scope and non-goals.
type Client interface {
	// Search runs a Soulseek search and returns results accumulated within
	// `window`. The window bounds how long we wait for peer responses.
	Search(ctx context.Context, query string, window time.Duration) ([]SearchResult, error)

	// Download initiates a transfer from `peer` for `filename`. Returns an
	// opaque handle the caller uses for DownloadStatus.
	Download(ctx context.Context, peer, filename string, size int64) (DownloadHandle, error)

	// DownloadStatus returns the current state of an in-flight or completed
	// download. State "completed" populates FilePath with the final path on
	// the shared music volume.
	DownloadStatus(ctx context.Context, h DownloadHandle) (DownloadState, error)
}

// SearchResult is a single peer's advertised file for a query.
type SearchResult struct {
	Peer        string
	Filename    string
	Size        int64
	Bitrate     int
	QueueLen    int
	FilesShared int
}

// DownloadHandle identifies an in-flight or completed download via a
// backend-opaque transfer ID.
type DownloadHandle struct {
	ID string
}

// DownloadState captures progress and, on completion, the resulting path.
type DownloadState struct {
	State    DownloadStateKind
	Bytes    int64
	Size     int64
	FilePath string
}

type DownloadStateKind string

const (
	DownloadQueued       DownloadStateKind = "queued"
	DownloadTransferring DownloadStateKind = "transferring"
	DownloadCompleted    DownloadStateKind = "completed"
	DownloadFailed       DownloadStateKind = "failed"
)
