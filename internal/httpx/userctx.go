// Package httpx holds HTTP-layer helpers: middleware, CORS, error rendering,
// and the request-context conventions for passing userID from middleware to handlers.
//
// Convention (see CLAUDE.md): handlers for protected routes extract userID via
// GetUserID(ctx). Middleware WithAuth is the only path that calls WithUserID.
// Domain services accept userID uuid.UUID as their first business argument;
// they never reach back into the *http.Request for identity.
package httpx

import (
	"context"

	"github.com/google/uuid"
)

type ctxKey int

const userIDKey ctxKey = 0

// WithUserID attaches a validated user ID to ctx. Only middleware should call this.
func WithUserID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, userIDKey, id)
}

// GetUserID pulls the user ID placed by WithAuth. Returns (zero, false) if absent.
func GetUserID(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(userIDKey).(uuid.UUID)
	return id, ok
}
