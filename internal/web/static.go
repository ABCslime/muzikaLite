// Package web serves the embedded Vue SPA.
//
// The Docker build copies frontend/dist/ into internal/web/dist/ before
// compiling the binary. In local development with `go run`, dist/ is empty
// (just a .keep sentinel), and SPAHandler returns 404 for all requests.
// Developers run `vite dev` on :3000 directly and proxy /api/* to :8080.
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:dist
var distFS embed.FS

// SPAHandler returns an http.Handler that serves the embedded Vue build.
// If dist/ is empty (dev mode), every request yields 404 — the dev server
// is responsible for serving the SPA on :3000.
func SPAHandler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// all:dist always exists; this error path is effectively unreachable.
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "spa not built", http.StatusNotFound)
		})
	}

	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Detect empty dist (dev mode) by checking for index.html.
		if _, err := fs.Stat(sub, "index.html"); err != nil {
			http.NotFound(w, r)
			return
		}

		// SPA fallback: if the requested path doesn't exist as a file,
		// serve index.html so Vue Router can handle client-side routes.
		p := r.URL.Path
		if p == "" || p == "/" {
			p = "/index.html"
			r = cloneRequestWithPath(r, p)
		} else if _, err := fs.Stat(sub, p[1:]); err != nil {
			r = cloneRequestWithPath(r, "/index.html")
		}
		fileServer.ServeHTTP(w, r)
	})
}

func cloneRequestWithPath(r *http.Request, path string) *http.Request {
	r2 := r.Clone(r.Context())
	r2.URL.Path = path
	return r2
}
