// Package web serves the embedded Vue SPA.
//
// Source lives in the top-level `frontend/` directory. Build output is
// staged into dist/ before `go build` so //go:embed bakes it into the
// released binary:
//
//	cd frontend && npm ci && npm run build
//	cp -r frontend/dist/* internal/web/dist/
//
// dist/ ships with a .gitkeep so the //go:embed all:dist directive
// stays valid on a clean checkout before the frontend has been built.
// In that state SPAHandler serves a 404 at /; /api/* still works.
// Developers running `vite dev` on :3000 proxy /api/* to :8080 and
// never hit the Go binary at /.
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:dist
var distFS embed.FS

// SPAHandler returns an http.Handler that serves the embedded Vue build.
// On a clean checkout dist/ contains only .gitkeep; in that state the
// handler returns 404 at /. After `npm run build` + the copy step,
// dist/index.html exists and SPA fallback is active.
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
		// Clean checkout or dev env where the frontend hasn't been built.
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
