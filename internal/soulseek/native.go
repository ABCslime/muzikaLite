package soulseek

import (
	"context"
	"time"
)

// NativeClient is a placeholder for the gosk-backed implementation.
// Until gosk (github.com/<user>/gosk) reaches its v1 scope goals (see
// ARCHITECTURE.md §7), this always returns ErrNotImplemented, and
// cmd/muzika/main.go refuses to start with SOULSEEK_BACKEND=native.
type NativeClient struct{}

// NewNativeClient constructs a placeholder native client.
func NewNativeClient() *NativeClient { return &NativeClient{} }

var _ Client = (*NativeClient)(nil)

func (*NativeClient) Search(ctx context.Context, query string, window time.Duration) ([]SearchResult, error) {
	return nil, ErrNotImplemented
}

func (*NativeClient) Download(ctx context.Context, peer, filename string, size int64) (DownloadHandle, error) {
	return DownloadHandle{}, ErrNotImplemented
}

func (*NativeClient) DownloadStatus(ctx context.Context, h DownloadHandle) (DownloadState, error) {
	return DownloadState{}, ErrNotImplemented
}
