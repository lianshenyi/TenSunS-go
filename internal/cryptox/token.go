package cryptox

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const tokenTTL = 8 * time.Hour

type tokenHeader struct {
	Alg string `json:"alg"`
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
}

// TokenSigner is compatible with itsdangerous'
// TimedJSONWebSignatureSerializer(secret_key, expires_in=28800) using HS512.
type TokenSigner struct {
	key []byte
	now func() time.Time
}

// NewTokenSigner creates a signer from the raw skey string.
func NewTokenSigner(key string) *TokenSigner {
	return &TokenSigner{key: []byte(key), now: time.Now}
}

// Sign creates a JWS compact token whose payload is the JSON string passed in.
func (s *TokenSigner) Sign(payload string) (string, error) {
	now := s.now().Unix()
	header, err := marshalCompact(tokenHeader{
		Alg: "HS512",
		Iat: now,
		Exp: now + int64(tokenTTL/time.Second),
	})
	if err != nil {
		return "", err
	}
	body, err := marshalCompact(payload)
	if err != nil {
		return "", err
	}
	h := base64.RawURLEncoding.EncodeToString(header)
	p := base64.RawURLEncoding.EncodeToString(body)
	signingInput := h + "." + p
	mac := hmac.New(sha512.New, s.key)
	_, _ = mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + sig, nil
}

// Verify checks the HS512 signature and expiry, then returns the JSON payload.
// It intentionally does not require the payload to equal the current password,
// matching the Python login_required behavior.
func (s *TokenSigner) Verify(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errors.New("malformed token")
	}
	headerBytes, err := decodeBase64URL(parts[0])
	if err != nil {
		return "", fmt.Errorf("decode header: %w", err)
	}
	sig, err := decodeBase64URL(parts[2])
	if err != nil {
		return "", fmt.Errorf("decode signature: %w", err)
	}
	mac := hmac.New(sha512.New, s.key)
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return "", errors.New("bad signature")
	}
	var header tokenHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return "", fmt.Errorf("decode header JSON: %w", err)
	}
	if header.Alg != "HS512" {
		return "", errors.New("unexpected token algorithm")
	}
	if header.Exp == 0 {
		return "", errors.New("missing token expiry")
	}
	if header.Exp < s.now().Unix() {
		return "", errors.New("token expired")
	}
	body, err := decodeBase64URL(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode payload: %w", err)
	}
	var payload string
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("decode payload JSON: %w", err)
	}
	return payload, nil
}

func decodeBase64URL(value string) ([]byte, error) {
	if len(value)%4 != 0 {
		value += strings.Repeat("=", 4-len(value)%4)
	}
	return base64.URLEncoding.DecodeString(value)
}
