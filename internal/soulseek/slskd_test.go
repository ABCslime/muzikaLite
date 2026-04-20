package soulseek_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/macabc/muzika/internal/soulseek"
)

// fakeSlskdServer models the subset of slskd's HTTP API our client calls.
// It's a small state machine: login issues a token, searches get created and
// marked complete after N polls, responses are returned from a fixture, and
// download status progresses from Queued → InProgress → Completed.
type fakeSlskdServer struct {
	token         string
	responses     []map[string]any
	searchPolls   atomic.Int32
	searchMustBe  int32 // after this many polls, report isComplete=true
	downloadState atomic.Int32
	downloadSteps []string // state strings to yield, cycled
}

func newFakeServer() *fakeSlskdServer {
	return &fakeSlskdServer{
		token:        "test-token",
		searchMustBe: 1,
		downloadSteps: []string{
			"Queued",
			"InProgress",
			"Completed, Succeeded",
		},
	}
}

func (f *fakeSlskdServer) mux() *http.ServeMux {
	m := http.NewServeMux()
	m.HandleFunc("/api/v0/session", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]string
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["username"] == "" || req["password"] == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"token": f.token})
	})
	m.HandleFunc("/api/v0/searches", func(w http.ResponseWriter, r *http.Request) {
		if !f.authed(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusCreated)
	})
	m.HandleFunc("/api/v0/searches/", func(w http.ResponseWriter, r *http.Request) {
		if !f.authed(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// Match: /api/v0/searches/<id>  OR /api/v0/searches/<id>/responses
		path := r.URL.Path
		if strings.HasSuffix(path, "/responses") {
			_ = json.NewEncoder(w).Encode(f.responses)
			return
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		polls := f.searchPolls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":         "id",
			"isComplete": polls >= f.searchMustBe,
		})
	})
	m.HandleFunc("/api/v0/transfers/downloads/", func(w http.ResponseWriter, r *http.Request) {
		if !f.authed(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			return
		}
		// GET: return the download list with state progressing per call.
		idx := f.downloadState.Add(1)
		if int(idx) > len(f.downloadSteps) {
			idx = int32(len(f.downloadSteps))
		}
		state := f.downloadSteps[idx-1]
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"id":               "dl-1",
				"state":            state,
				"size":             int64(1234),
				"bytesTransferred": int64(idx) * 411,
				"filename":         `@@peer\\dir\\song.mp3`,
			},
		})
	})
	return m
}

func (f *fakeSlskdServer) authed(r *http.Request) bool {
	return r.Header.Get("Authorization") == "Bearer "+f.token
}

func TestSlskd_LoginAndSearch(t *testing.T) {
	f := newFakeServer()
	f.responses = []map[string]any{
		{
			"username":    "peer1",
			"queueLength": 0,
			"fileCount":   1000,
			"files": []map[string]any{
				{"filename": `@@peer1\\song.mp3`, "size": 5000, "bitRate": 320},
			},
		},
	}
	srv := httptest.NewServer(f.mux())
	defer srv.Close()

	client := soulseek.NewSlskdClient(srv.URL, "muzika", "devpassword")
	results, err := client.Search(context.Background(), "Some Artist Some Title", 2*time.Second)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	r := results[0]
	if r.Peer != "peer1" || r.Size != 5000 || r.FilesShared != 1000 {
		t.Errorf("unexpected result: %+v", r)
	}
}

func TestSlskd_DownloadStatusFlow(t *testing.T) {
	f := newFakeServer()
	srv := httptest.NewServer(f.mux())
	defer srv.Close()

	client := soulseek.NewSlskdClient(srv.URL, "u", "p")
	// Force a login (Search would normally do this for us).
	_, _ = client.Search(context.Background(), "x", 1*time.Second)

	h, err := client.Download(context.Background(), "peer1", `@@peer\\dir\\song.mp3`, 1234)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}

	// Poll at least 3 times to walk the state machine.
	var states []soulseek.DownloadStateKind
	for i := 0; i < 3; i++ {
		st, err := client.DownloadStatus(context.Background(), h)
		if err != nil {
			t.Fatalf("DownloadStatus: %v", err)
		}
		states = append(states, st.State)
		if st.State == soulseek.DownloadCompleted {
			if st.FilePath != "song.mp3" {
				t.Errorf("filePath not basename-normalized: %q", st.FilePath)
			}
			return
		}
	}
	t.Errorf("never reached Completed state: %v", states)
}

func TestSlskd_401RefreshesToken(t *testing.T) {
	f := newFakeServer()
	srv := httptest.NewServer(f.mux())
	defer srv.Close()

	client := soulseek.NewSlskdClient(srv.URL, "u", "p")

	// First call: no token yet → client logs in lazily.
	_, err := client.Search(context.Background(), "q", 1*time.Second)
	if err != nil {
		t.Fatalf("first search: %v", err)
	}

	// Rotate the server's token behind the client's back. Next request uses
	// the old token, server returns 401, client re-logs in and retries.
	f.token = "rotated-token"
	f.searchPolls.Store(0)

	_, err = client.Search(context.Background(), "q2", 1*time.Second)
	if err != nil {
		t.Fatalf("post-rotation search: %v", err)
	}
}
