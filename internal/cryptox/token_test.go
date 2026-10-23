package cryptox

import (
	"strings"
	"testing"
	"time"
)

func fixedSigner() *TokenSigner {
	s := NewTokenSigner(testKey)
	s.now = func() time.Time { return time.Unix(1700000000, 0) }
	return s
}

// Produced by itsdangerous 2.0.1:
// TimedJSONWebSignatureSerializer(key, expires_in=28800).dumps("secret-password")
const pythonToken = "eyJhbGciOiJIUzUxMiIsImlhdCI6MTcwMDAwMDAwMCwiZXhwIjoxNzAwMDI4ODAwfQ.InNlY3JldC1wYXNzd29yZCI.31UHMD59zl1Gd8mFOIGesQiQ_mJqWgd2afAKZryKZZDUE4GZLKw197TZe-r9tyL3i4WyQS1FXx8QZbdN6FTNGw"

func TestSignMatchesItsdangerous(t *testing.T) {
	got, err := fixedSigner().Sign("secret-password")
	if err != nil {
		t.Fatal(err)
	}
	if got != pythonToken {
		t.Fatalf("token mismatch\n got: %s\nwant: %s", got, pythonToken)
	}
}

func TestVerifyPythonToken(t *testing.T) {
	payload, err := fixedSigner().Verify(pythonToken)
	if err != nil {
		t.Fatal(err)
	}
	if payload != "secret-password" {
		t.Fatalf("payload = %q", payload)
	}
}

func TestVerifyRejectsExpiredAndTampered(t *testing.T) {
	s := fixedSigner()
	s.now = func() time.Time { return time.Unix(1700028801, 0) }
	if _, err := s.Verify(pythonToken); err == nil {
		t.Fatal("expected expiry error")
	}

	s = fixedSigner()
	parts := strings.Split(pythonToken, ".")
	sig := []byte(parts[2])
	if sig[0] == 'A' {
		sig[0] = 'B'
	} else {
		sig[0] = 'A'
	}
	tampered := parts[0] + "." + parts[1] + "." + string(sig)
	if _, err := s.Verify(tampered); err == nil {
		t.Fatal("expected signature error")
	}
}

func TestSignEscapesNoHTML(t *testing.T) {
	// itsdangerous uses ensure_ascii=False and no HTML escaping.
	const expected = "eyJhbGciOiJIUzUxMiIsImlhdCI6MTcwMDAwMDAwMCwiZXhwIjoxNzAwMDI4ODAwfQ.Ijwm5Lit5paHIg.FGJr2fMGRSLlv_1gEXRmEDM_G2MvgnUHxQ0yJaseRfgPtrJu7R9aPafuNIE2AEjxcHaG_uPj1VAquOvLfNWQFg"
	got, err := fixedSigner().Sign("<&中文")
	if err != nil {
		t.Fatal(err)
	}
	if got != expected {
		t.Fatalf("token mismatch\n got: %s\nwant: %s", got, expected)
	}
}
