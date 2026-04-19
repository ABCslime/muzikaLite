// Package soulseek defines the abstraction over Soulseek backends.
//
// Two implementations live here:
//   - slskd.go   — HTTP client against the slskd daemon. Ships day one.
//   - native.go  — gosk (github.com/<user>/gosk) — returns ErrNotImplemented
//                  in v1. Will be enabled when gosk reaches its scope goals.
//
// The selector in cmd/muzika/main.go picks one based on MUZIKA_SOULSEEK_BACKEND.
package soulseek

import (
	"context"
	"errors"
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

// DownloadHandle is a backend-opaque identifier.
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

// ErrNotImplemented is returned by the native backend while gosk is unfinished.
var ErrNotImplemented = errors.New("soulseek: backend not implemented")
