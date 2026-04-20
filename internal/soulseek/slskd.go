package soulseek

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// SlskdClient implements Client by talking to the slskd daemon's HTTP API.
// Ships day one. See ARCHITECTURE.md §7.
//
// Token handling: login is deferred to the first call; 401 responses trigger
// a re-login and one retry. Credentials come from MUZIKA_SLSKD_USERNAME /
// MUZIKA_SLSKD_PASSWORD (config.go).
type SlskdClient struct {
	baseURL    string
	username   string
	password   string
	httpClient *http.Client

	mu    sync.RWMutex
	token string
}

// NewSlskdClient constructs a client. The default daemon port is 5030
// (matches the sidecar in docker-compose.yml).
func NewSlskdClient(baseURL, username, password string) *SlskdClient {
	return &SlskdClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		username:   username,
		password:   password,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Compile-time interface check.
var _ Client = (*SlskdClient)(nil)

// ---------- authentication ----------

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token string `json:"token"`
}

func (c *SlskdClient) login(ctx context.Context) error {
	body, err := json.Marshal(loginRequest{Username: c.username, Password: c.password})
	if err != nil {
		return fmt.Errorf("marshal login: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/api/v0/session", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("new login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("login: http %d", resp.StatusCode)
	}
	var lr loginResponse
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return fmt.Errorf("decode login: %w", err)
	}
	c.mu.Lock()
	c.token = lr.Token
	c.mu.Unlock()
	return nil
}

// do issues an authenticated request, logging in lazily and retrying once on 401.
func (c *SlskdClient) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var bodyRdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		bodyRdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyRdr)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c.mu.RLock()
	tok := c.token
	c.mu.RUnlock()
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}
	// 401 → re-login and retry once.
	_ = resp.Body.Close()
	if err := c.login(ctx); err != nil {
		return nil, err
	}
	return c.retryOnce(ctx, method, path, body)
}

func (c *SlskdClient) retryOnce(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var bodyRdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		bodyRdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyRdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c.mu.RLock()
	tok := c.token
	c.mu.RUnlock()
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return c.httpClient.Do(req)
}

// ---------- Search ----------

type slskdSearchRequest struct {
	ID         string `json:"id"`
	SearchText string `json:"searchText"`
}

type slskdSearch struct {
	ID         string `json:"id"`
	IsComplete bool   `json:"isComplete"`
}

type slskdResponse struct {
	Username     string      `json:"username"`
	QueueLength  int         `json:"queueLength"`
	FileCount    int         `json:"fileCount"`
	Files        []slskdFile `json:"files"`
}

type slskdFile struct {
	Filename  string `json:"filename"`
	Size      int64  `json:"size"`
	BitRate   int    `json:"bitRate"`
}

// Search creates a search, polls until complete (or `window` elapses), and
// fetches responses. Results are flattened to one SearchResult per file.
func (c *SlskdClient) Search(ctx context.Context, query string, window time.Duration) ([]SearchResult, error) {
	// Lazy login.
	c.mu.RLock()
	tok := c.token
	c.mu.RUnlock()
	if tok == "" {
		if err := c.login(ctx); err != nil {
			return nil, err
		}
	}

	searchID := newSearchID()
	body := slskdSearchRequest{ID: searchID, SearchText: query}
	resp, err := c.do(ctx, http.MethodPost, "/api/v0/searches", body)
	if err != nil {
		return nil, fmt.Errorf("create search: %w", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("create search: http %d", resp.StatusCode)
	}

	// Poll until isComplete or window elapses.
	deadline := time.Now().Add(window)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		done, err := c.searchComplete(ctx, searchID)
		if err != nil {
			return nil, err
		}
		if done {
			break
		}
		if time.Now().After(deadline) {
			break // give up polling; fetch whatever responses we have
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}

	responses, err := c.searchResponses(ctx, searchID)
	if err != nil {
		// Even on response-fetch failure, still clean up the server-side
		// search handle so slskd doesn't accumulate orphans.
		c.deleteSearchDetached(searchID)
		return nil, err
	}

	// Best-effort cleanup. Detach from the caller's ctx: if the caller
	// cancelled (client went away, timeout elapsed), we still want slskd to
	// drop the search handle — a 5 s timeout bounds how long we'll try.
	c.deleteSearchDetached(searchID)

	// Flatten into SearchResult slice.
	var out []SearchResult
	for _, r := range responses {
		for _, f := range r.Files {
			out = append(out, SearchResult{
				Peer:        r.Username,
				Filename:    f.Filename,
				Size:        f.Size,
				Bitrate:     f.BitRate,
				QueueLen:    r.QueueLength,
				FilesShared: r.FileCount,
			})
		}
	}
	return out, nil
}

func (c *SlskdClient) searchComplete(ctx context.Context, id string) (bool, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/v0/searches/"+id, nil)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("poll search: http %d", resp.StatusCode)
	}
	var s slskdSearch
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return false, fmt.Errorf("decode search: %w", err)
	}
	return s.IsComplete, nil
}

// deleteSearchDetached issues DELETE /api/v0/searches/<id> on a background
// context with a 5 s timeout. Called after Search returns (including the
// error paths) so a cancelled caller ctx still cleans up the server-side
// handle. Errors are ignored — this is best-effort janitorial work.
func (c *SlskdClient) deleteSearchDetached(searchID string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		resp, err := c.do(ctx, http.MethodDelete, "/api/v0/searches/"+searchID, nil)
		if err == nil && resp != nil {
			_ = resp.Body.Close()
		}
	}()
}

func (c *SlskdClient) searchResponses(ctx context.Context, id string) ([]slskdResponse, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/v0/searches/"+id+"/responses", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch responses: http %d", resp.StatusCode)
	}
	var out []slskdResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode responses: %w", err)
	}
	return out, nil
}

// ---------- Download ----------

type slskdDownloadRequest struct {
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
}

type slskdDownload struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	Size      int64  `json:"size"`
	Transferred int64 `json:"bytesTransferred"`
	Filename  string `json:"filename"`
}

// Download enqueues a transfer from `peer` for `filename`. The returned handle
// carries the (peer, filename) pair verbatim; DownloadStatus uses them to
// query progress. No encoding/separator scheme is needed — the fields are
// preserved as-is on the struct.
func (c *SlskdClient) Download(ctx context.Context, peer, filename string, size int64) (DownloadHandle, error) {
	body := []slskdDownloadRequest{{Filename: filename, Size: size}}
	resp, err := c.do(ctx, http.MethodPost, "/api/v0/transfers/downloads/"+peer, body)
	if err != nil {
		return DownloadHandle{}, fmt.Errorf("start download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return DownloadHandle{}, fmt.Errorf("start download: http %d", resp.StatusCode)
	}
	return DownloadHandle{Peer: peer, Filename: filename}, nil
}

// DownloadStatus polls the downloads list and returns the current state.
func (c *SlskdClient) DownloadStatus(ctx context.Context, h DownloadHandle) (DownloadState, error) {
	if h.Peer == "" || h.Filename == "" {
		return DownloadState{}, fmt.Errorf("invalid handle: peer=%q filename=%q", h.Peer, h.Filename)
	}
	peer, filename := h.Peer, h.Filename
	resp, err := c.do(ctx, http.MethodGet, "/api/v0/transfers/downloads/"+peer, nil)
	if err != nil {
		return DownloadState{}, fmt.Errorf("download status: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return DownloadState{}, fmt.Errorf("download status: http %d", resp.StatusCode)
	}
	var downloads []slskdDownload
	if err := json.NewDecoder(resp.Body).Decode(&downloads); err != nil {
		return DownloadState{}, fmt.Errorf("decode: %w", err)
	}
	for _, d := range downloads {
		if d.Filename != filename {
			continue
		}
		return DownloadState{
			State:    mapSlskdState(d.State),
			Bytes:    d.Transferred,
			Size:     d.Size,
			FilePath: filenameToLocalPath(d.Filename),
		}, nil
	}
	return DownloadState{}, errors.New("soulseek: download not found in list")
}

// mapSlskdState translates slskd's state strings to our DownloadStateKind.
// slskd states include "Completed, Succeeded", "InProgress", "Queued",
// "Completed, Cancelled", "Completed, Errored", etc. We match prefix-wise.
func mapSlskdState(s string) DownloadStateKind {
	switch {
	case strings.Contains(s, "Completed, Succeeded"):
		return DownloadCompleted
	case strings.HasPrefix(s, "Completed"):
		return DownloadFailed
	case strings.Contains(s, "Queued"):
		return DownloadQueued
	case strings.Contains(s, "InProgress"), strings.Contains(s, "Transferring"):
		return DownloadTransferring
	case strings.Contains(s, "Errored"), strings.Contains(s, "Failed"), strings.Contains(s, "Cancelled"):
		return DownloadFailed
	default:
		return DownloadStateKind(strings.ToLower(s))
	}
}

// filenameToLocalPath converts slskd's remote filename (backslash-separated)
// to a local POSIX path relative to the shared downloads volume. slskd writes
// the final file under /downloads/<last-path-segment-or-preserved-structure>.
//
// Behavior matches the old Java code: use the basename.
func filenameToLocalPath(remote string) string {
	// slskd file names often look like "@@abc\\Artist\\Album\\01 Track.mp3"
	remote = strings.ReplaceAll(remote, "\\", "/")
	idx := strings.LastIndex(remote, "/")
	if idx == -1 {
		return remote
	}
	return remote[idx+1:]
}

// newSearchID returns a fresh search ID. We use a UUID each time (replaces
// the old SlskdDownloader 8-UUID hardcoded pool).
func newSearchID() string {
	return uuidPkgNewString()
}
