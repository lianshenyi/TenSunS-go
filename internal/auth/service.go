// Package auth implements the single-admin login and bearer-token checks.
package auth

import (
	"crypto/subtle"
	"errors"
	"strings"

	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
)

// Service owns the admin password and the compatible token signer.
type Service struct {
	password string
	tokens   *cryptox.TokenSigner
}

func New(password string, tokens *cryptox.TokenSigner) *Service {
	return &Service{password: password, tokens: tokens}
}

// CheckPassword uses a constant-time comparison.
func (s *Service) CheckPassword(password string) bool {
	return subtle.ConstantTimeCompare([]byte(password), []byte(s.password)) == 1
}

// NewToken returns the compact token without the Bearer prefix.
func (s *Service) NewToken() (string, error) {
	return s.tokens.Sign(s.password)
}

// ParseAuthorization extracts and verifies an HTTP Authorization header.
// Flask-HTTPAuth's token scheme is case-insensitive; a missing scheme is
// invalid even if the JWS itself is well-formed.
func (s *Service) ParseAuthorization(header string) (string, error) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", errors.New("missing bearer token")
	}
	return s.tokens.Verify(parts[1])
}
