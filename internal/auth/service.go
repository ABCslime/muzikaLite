package auth

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/bus"
)

// Service holds the auth module's dependencies and exposes the business
// surface the HTTP handlers call. See CLAUDE.md for the handler ↔ service
// contract (handlers pass userID; services never touch *http.Request).
type Service struct {
	db      *sql.DB
	repo    *Repo
	jwt     *JWT
	bus     *bus.Bus
	dispatcher *bus.OutboxDispatcher
}

// NewService wires the pieces. Handler/JWT verifier are exposed via accessors.
func NewService(db *sql.DB, secret string, expiration time.Duration, b *bus.Bus, d *bus.OutboxDispatcher) *Service {
	repo := NewRepo(db)
	s := &Service{db: db, repo: repo, bus: b, dispatcher: d}
	s.jwt = NewJWT(secret, expiration, repo.GetTokenVersion)
	return s
}

// Verifier exposes the JWT as an httpx.JWTVerifier for middleware use.
func (s *Service) Verifier() *JWT { return s.jwt }

// Register creates a user, emits UserCreated to the outbox, returns the new row.
// TODO(port): Phase 4.
func (s *Service) Register(ctx context.Context, req RegisterRequest) (User, error) {
	return User{}, errors.New("auth.Service.Register: not implemented")
}

// Login verifies credentials and issues a JWT.
// TODO(port): Phase 4.
func (s *Service) Login(ctx context.Context, req LoginRequest) (LoginResponse, error) {
	return LoginResponse{}, errors.New("auth.Service.Login: not implemented")
}

// LogoutAll bumps the user's token_version; all outstanding tokens fail Verify.
// TODO(port): Phase 4.
func (s *Service) LogoutAll(ctx context.Context, userID uuid.UUID) error {
	return errors.New("auth.Service.LogoutAll: not implemented")
}

// Delete removes a user. FK cascade cleans up dependents; emits UserDeleted.
// TODO(port): Phase 4.
func (s *Service) Delete(ctx context.Context, userID uuid.UUID) error {
	return errors.New("auth.Service.Delete: not implemented")
}
