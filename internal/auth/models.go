// Package auth implements user registration, login, JWT issuance, and
// cascading deletion. It's the identity provider for the other modules.
//
// Phase 3 scaffold: interfaces and stubs only. Phase 4 ports business logic.
package auth

import (
	"time"

	"github.com/google/uuid"
)

// User is the in-memory shape of an auth_users row.
type User struct {
	ID           uuid.UUID
	Username     string
	PasswordHash string
	Email        string
	TokenVersion int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// RegisterRequest is the POST /api/auth/user body.
type RegisterRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Email    string `json:"email,omitempty"`
}

// LoginRequest is the POST /api/auth/login body.
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResponse mirrors the old Spring service's response shape so the Vue
// frontend works unchanged.
type LoginResponse struct {
	Token    string    `json:"token"`
	UserID   uuid.UUID `json:"userId"`
	Username string    `json:"username"`
	Email    string    `json:"email,omitempty"`
}

// UserResponse is the JSON shape returned from register/user endpoints.
type UserResponse struct {
	ID        uuid.UUID `json:"id"`
	Username  string    `json:"username"`
	Email     string    `json:"email,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}
