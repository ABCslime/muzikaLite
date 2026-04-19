package httpx

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/google/uuid"
)

// JWTVerifier is the subset of the auth package used by WithAuth.
// Defined here to keep httpx free of the auth package import.
type JWTVerifier interface {
	// Verify validates the token string, enforces the tv claim against the
	// current stored token_version, and returns the userID on success.
	Verify(token string) (uuid.UUID, error)
}

// WithAuth returns a middleware that requires a valid JWT bearer token.
// Public routes mount their handler directly; protected routes wrap with this.
func WithAuth(v JWTVerifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				WriteError(w, http.StatusUnauthorized, "missing bearer token")
				return
			}
			userID, err := v.Verify(token)
			if err != nil {
				WriteError(w, http.StatusUnauthorized, "invalid or revoked token")
				return
			}
			next.ServeHTTP(w, r.WithContext(WithUserID(r.Context(), userID)))
		})
	}
}

func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(h[len(prefix):]), true
}

// Recover catches panics in downstream handlers, logs the stack, and returns 500.
func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error("panic in handler",
						"path", r.URL.Path, "method", r.Method,
						"recover", rec, "stack", string(debug.Stack()))
					WriteError(w, http.StatusInternalServerError, "internal server error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// RequestLog emits one structured log line per request.
func RequestLog(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: 200}
			next.ServeHTTP(sw, r)
			log.Info("http",
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.status,
				"dur_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}
