package soulseek

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ABCslime/gosk"
)

// NativeClient adapts github.com/ABCslime/gosk to this package's Client
// interface. As of v0.3.0 this is the only backend we ship — the HTTP
// client against the slskd daemon was retired so muzika can run as a
// single binary on the Pi with no sidecar.
//
// Field-for-field translation only — gosk defines its public API with
// matching semantics by design (see gosk/README.md wire-up section).
//
// Login is lazy: the first call that requires a session triggers it,
// which keeps main.go wiring uniform even though there's only one
// backend now.
type NativeClient struct {
	g *gosk.Client

	// sessionCtx scopes the lifetime of the Soulseek session (the TCP
	// connection to the Soulseek server, plus the read/write/listen/
	// deserialize goroutines inside soul that depend on it).
	//
	// CRITICAL: this MUST NOT be the ctx of any individual caller
	// (HTTP request, background worker). If Login were given a
	// request ctx via sync.Once, the first request to arrive would
	// have its ctx bound to the session for the rest of the process.
	// When that request completes, its ctx cancels, soul's Dial
	// cleanup goroutine fires c.close(), the TCP socket closes, and
	// every search from then on silently fails because the write
	// goroutine is gone and the Writer channel reader is dead.
	//
	// Separate context, cancelled only in Close(), keeps the session
	// alive across every request that comes through the single
	// shared NativeClient instance.
	sessionCtx    context.Context
	sessionCancel context.CancelFunc

	loginOnce sync.Once
	loginErr  error

	// handleFilenames remembers the raw peer-supplied filename per download
	// handle. It's keyed by the handle ID returned from gosk.Download and
	// consumed once at completion by DownloadStatus. See DownloadStatus for
	// why we need this.
	mu              sync.Mutex
	handleFilenames map[string]string
}

// NewNativeClient constructs a NativeClient wrapping cfg. Login happens on
// first use, not here.
func NewNativeClient(cfg *gosk.Config) (*NativeClient, error) {
	g, err := gosk.New(cfg)
	if err != nil {
		return nil, err
	}
	sessionCtx, sessionCancel := context.WithCancel(context.Background())
	return &NativeClient{
		g:               g,
		sessionCtx:      sessionCtx,
		sessionCancel:   sessionCancel,
		handleFilenames: make(map[string]string),
	}, nil
}

// ensureLogin triggers the one-shot login on first use.
//
// The caller's ctx is IGNORED for the Login call itself — we use the
// long-lived sessionCtx so the session outlives any individual request.
// If the caller's ctx cancels mid-login (user closes the tab while a
// search is waiting for first-boot), Login continues to completion in
// the background; the caller sees the normal search-timeout path but
// the next request finds a ready session.
func (n *NativeClient) ensureLogin(_ context.Context) error {
	n.loginOnce.Do(func() {
		n.loginErr = n.g.Login(n.sessionCtx)
	})
	return n.loginErr
}

// Close tears down the underlying client and the session context. Safe
// to call once, at process shutdown.
func (n *NativeClient) Close() error {
	n.sessionCancel()
	return n.g.Close()
}

// Inner exposes the wrapped *gosk.Client for callers that want direct access
// (e.g. for Resume on startup).
func (n *NativeClient) Inner() *gosk.Client { return n.g }

var _ Client = (*NativeClient)(nil)

// ---- Client interface ----

func (n *NativeClient) Search(ctx context.Context, query string, window time.Duration) ([]SearchResult, error) {
	if err := n.ensureLogin(ctx); err != nil {
		return nil, err
	}
	res, err := n.g.Search(ctx, query, window)
	if err != nil {
		return nil, err
	}
	out := make([]SearchResult, len(res))
	for i, r := range res {
		out[i] = SearchResult{
			Peer:        r.Peer,
			Filename:    r.Filename,
			Size:        r.Size,
			Bitrate:     r.Bitrate,
			QueueLen:    r.QueueLen,
			FilesShared: r.FilesShared,
		}
	}
	return out, nil
}

func (n *NativeClient) Download(ctx context.Context, peer, filename string, size int64) (DownloadHandle, error) {
	if err := n.ensureLogin(ctx); err != nil {
		return DownloadHandle{}, err
	}
	h, err := n.g.Download(ctx, peer, filename, size)
	if err != nil {
		return DownloadHandle{}, err
	}
	n.mu.Lock()
	n.handleFilenames[h.ID] = filename
	n.mu.Unlock()
	return DownloadHandle{ID: h.ID}, nil
}

func (n *NativeClient) DownloadStatus(ctx context.Context, h DownloadHandle) (DownloadState, error) {
	s, err := n.g.DownloadStatus(ctx, gosk.DownloadHandle{ID: h.ID})
	if err != nil {
		return DownloadState{}, err
	}
	out := DownloadState{
		State:    translateState(s.State),
		Bytes:    s.Bytes,
		Size:     s.Size,
		FilePath: s.FilePath,
	}

	// gosk v0.1.0 has an asymmetry: its inner session writes the file to
	// disk using the raw peer-supplied filename verbatim — on POSIX the
	// backslashes inside a Soulseek share path ("music\\Artist\\Album\\…")
	// are literal filename characters, so the file ends up at one long
	// mangled name. But gosk's bookkeeping layer (finishDownload) reports
	// FilePath as DownloadFolder + sanitized basename, which doesn't match
	// what's actually on disk.
	//
	// Reconcile at completion: rename the mangled on-disk file to match
	// FilePath, then hand the caller back the basename only. The caller
	// (queue.ResolveSongPath) joins it with musicStoragePath, so returning
	// the full path would double-prefix.
	if out.State == DownloadCompleted && out.FilePath != "" {
		n.mu.Lock()
		rawName, hadRaw := n.handleFilenames[h.ID]
		delete(n.handleFilenames, h.ID)
		n.mu.Unlock()

		if _, statErr := os.Stat(out.FilePath); os.IsNotExist(statErr) && hadRaw {
			rawOnDisk := filepath.Join(filepath.Dir(out.FilePath), rawName)
			if _, e := os.Stat(rawOnDisk); e == nil {
				_ = os.Rename(rawOnDisk, out.FilePath)
			}
		}
		out.FilePath = filepath.Base(out.FilePath)
	}

	// Release the handle-filename entry on terminal failure too, to avoid
	// a slow leak if the worker never polls a successful state.
	if out.State == DownloadFailed {
		n.mu.Lock()
		delete(n.handleFilenames, h.ID)
		n.mu.Unlock()
	}

	return out, nil
}

func translateState(s gosk.DownloadStateKind) DownloadStateKind {
	switch s {
	case gosk.DownloadQueued:
		return DownloadQueued
	case gosk.DownloadTransferring:
		return DownloadTransferring
	case gosk.DownloadCompleted:
		return DownloadCompleted
	case gosk.DownloadFailed:
		return DownloadFailed
	default:
		return DownloadStateKind(string(s))
	}
}
