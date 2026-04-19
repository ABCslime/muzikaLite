package httpx

import "net/http"

// CORSConfig holds the allow-list. Empty Origins means no CORS headers emitted
// (same-origin assumed — typical in production where the binary serves the SPA).
// No wildcards are ever produced, even if callers pass "*" — it's just ignored.
type CORSConfig struct {
	Origins []string
}

// CORS returns middleware that only reflects a request's Origin header back
// if it exactly matches one of the configured origins. Also handles OPTIONS
// preflight requests.
func CORS(cfg CORSConfig) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(cfg.Origins))
	for _, o := range cfg.Origins {
		if o == "" || o == "*" {
			continue
		}
		allowed[o] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if _, ok := allowed[origin]; ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
				w.Header().Set("Access-Control-Max-Age", "3600")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
