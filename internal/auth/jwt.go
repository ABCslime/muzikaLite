package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// TVLoader returns the current token_version for a user. JWT.Verify calls it
// on every verification to enforce revocation via /api/auth/logout-all.
type TVLoader func(ctx context.Context, id uuid.UUID) (int, error)

// JWT handles signing and verifying tokens. The `tv` claim carries the user's
// current token_version; mismatch with the DB row is treated as revoked.
// See ARCHITECTURE.md §6.
type JWT struct {
	secret     []byte
	expiration time.Duration
	tvLoader   TVLoader
}

// NewJWT constructs a manager. tvLoader is called on every Verify.
func NewJWT(secret string, expiration time.Duration, tvLoader TVLoader) *JWT {
	return &JWT{
		secret:     []byte(secret),
		expiration: expiration,
		tvLoader:   tvLoader,
	}
}

// Issue mints a token for userID at the given tokenVersion.
// TODO(port): implement in Phase 4.
func (j *JWT) Issue(userID uuid.UUID, tokenVersion int) (string, error) {
	return "", errors.New("auth.JWT.Issue: not implemented")
}

// Verify parses the token, checks signature + exp, then reads tv and calls
// tvLoader to confirm it matches the stored value. Returns the userID.
// TODO(port): implement in Phase 4.
func (j *JWT) Verify(token string) (uuid.UUID, error) {
	return uuid.Nil, errors.New("auth.JWT.Verify: not implemented")
}
