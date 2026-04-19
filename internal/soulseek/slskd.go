package soulseek

import (
	"context"
	"net/http"
	"time"
)

// SlskdClient implements Client by talking to the slskd daemon's HTTP API.
// Ships day one. See ARCHITECTURE.md §7.
type SlskdClient struct {
	baseURL    string
	username   string
	password   string
	httpClient *http.Client

	// Session token cache — populated lazily on first call; refreshed on 401.
	// TODO(port): fill in during Phase 7 (slskd module port).
}

// NewSlskdClient constructs a client. Credentials come from env (MUZIKA_SLSKD_*);
// the default daemon port is 5030 (matches the sidecar in docker-compose.yml).
func NewSlskdClient(baseURL, username, password string) *SlskdClient {
	return &SlskdClient{
		baseURL:    baseURL,
		username:   username,
		password:   password,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Compile-time interface check.
var _ Client = (*SlskdClient)(nil)

// Search is a stub until Phase 7.
func (c *SlskdClient) Search(ctx context.Context, query string, window time.Duration) ([]SearchResult, error) {
	return nil, ErrNotImplemented
}

// Download is a stub until Phase 7.
func (c *SlskdClient) Download(ctx context.Context, peer, filename string, size int64) (DownloadHandle, error) {
	return DownloadHandle{}, ErrNotImplemented
}

// DownloadStatus is a stub until Phase 7.
func (c *SlskdClient) DownloadStatus(ctx context.Context, h DownloadHandle) (DownloadState, error) {
	return DownloadState{}, ErrNotImplemented
}
