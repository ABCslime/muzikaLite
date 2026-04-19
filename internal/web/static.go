// Package web serves the embedded Vue SPA.
//
// The frontend is deferred to a post-backend phase. Until it lands,
// dist/ holds a committed placeholder index.html and a .gitkeep. The
// //go:embed all:dist directive requires at least one matching file;
// .gitkeep satisfies that invariant and is hidden so `cp -r dist/*`
// from a future `npm run build` step won't clobber it.
//
// When the frontend arrives, CI's "Stage frontend dist for embed" step
// overwrites index.html with the real Vue build output before `go build`.
// Developers running `vite dev` on :3000 proxy /api/* to :8080 and never
// hit the Go binary at /.
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:dist
var distFS embed.FS

// SPAHandler returns an http.Handler that serves the embedded Vue build.
// With the committed placeholder at dist/index.html, this always has
// something to serve; the 404 branch below is defensive cover in case
// someone ever wipes the placeholder.
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
		// Defensive: 404 if someone wiped the placeholder.
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
