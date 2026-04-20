package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// TVLoader returns the current token_version for a user. JWT.Verify calls it
// on every verification to enforce revocation via /api/auth/logout-all.
type TVLoader func(ctx context.Context, id uuid.UUID) (int, error)

// Errors returned by Verify. Middleware translates all three to HTTP 401.
var (
	ErrInvalidToken = errors.New("auth: invalid token")
	ErrExpiredToken = errors.New("auth: token expired")
	ErrRevokedToken = errors.New("auth: token revoked")
)

// jwtIssuer is the `iss` claim value. Set at mint time and enforced at
// verify time so a token minted by some other service with the same HS256
// secret can't be replayed here (and vice versa).
const jwtIssuer = "muzika"

// muzikaClaims is our JWT payload: sub=userID, tv=token_version, iat, exp, iss.
type muzikaClaims struct {
	TV int `json:"tv"`
	jwt.RegisteredClaims
}

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

// Issue mints a token for userID at the given tokenVersion. Uses HS256.
func (j *JWT) Issue(userID uuid.UUID, tokenVersion int) (string, error) {
	now := time.Now()
	claims := muzikaClaims{
		TV: tokenVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    jwtIssuer,
			Subject:   userID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(j.expiration)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := tok.SignedString(j.secret)
	if err != nil {
		return "", fmt.Errorf("jwt sign: %w", err)
	}
	return s, nil
}

// Verify parses the token, checks the HS256 signature and exp, reads the tv
// claim, and compares it to the current stored token_version. Returns the
// userID on success.
func (j *JWT) Verify(ctx context.Context, token string) (uuid.UUID, error) {
	var claims muzikaClaims
	parsed, err := jwt.ParseWithClaims(token, &claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return j.secret, nil
	}, jwt.WithIssuer(jwtIssuer))
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return uuid.Nil, ErrExpiredToken
		}
		return uuid.Nil, ErrInvalidToken
	}
	if !parsed.Valid {
		return uuid.Nil, ErrInvalidToken
	}
	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return uuid.Nil, ErrInvalidToken
	}
	currentTV, err := j.tvLoader(ctx, userID)
	if err != nil {
		// Treat "user not found" (user was deleted) as an invalid token —
		// don't leak existence vs. non-existence.
		return uuid.Nil, ErrInvalidToken
	}
	if claims.TV != currentTV {
		return uuid.Nil, ErrRevokedToken
	}
	return userID, nil
}
