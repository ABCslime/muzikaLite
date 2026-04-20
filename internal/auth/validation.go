package auth

import (
	"errors"
	"net/mail"
	"regexp"
)

// Validation errors. Handlers translate each to HTTP 400.
var (
	ErrInvalidUsername = errors.New("auth: username must be 3-64 chars, [a-zA-Z0-9_-]+")
	ErrInvalidPassword = errors.New("auth: password must be at least 8 characters")
	ErrInvalidEmail    = errors.New("auth: email is not a valid address")
)

var usernameRE = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func validateUsername(s string) error {
	if len(s) < 3 || len(s) > 64 {
		return ErrInvalidUsername
	}
	if !usernameRE.MatchString(s) {
		return ErrInvalidUsername
	}
	return nil
}

func validatePassword(s string) error {
	if len(s) < 8 {
		return ErrInvalidPassword
	}
	return nil
}

// validateEmail treats empty as valid (email is optional at this layer).
func validateEmail(s string) error {
	if s == "" {
		return nil
	}
	if _, err := mail.ParseAddress(s); err != nil {
		return ErrInvalidEmail
	}
	return nil
}
