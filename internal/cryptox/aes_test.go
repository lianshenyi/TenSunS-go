package cryptox

import (
	"bytes"
	"testing"
)

const testKey = "0123456789abcdef0123456789abcdef"

// Produced by Python cryptography with IV 00..0f and AES-256-CBC/PKCS#7.
const pythonAESVector = "AAECAwQFBgcICQoLDA0ODyXcGrlnQPo6HYFLE8tL5ws="

func TestDecryptPythonVector(t *testing.T) {
	c, err := NewCipher(testKey)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Decrypt(pythonAESVector)
	if err != nil {
		t.Fatal(err)
	}
	if got != "AKID示例<>&" {
		t.Fatalf("got %q", got)
	}
}

func TestEncryptMatchesPythonFormat(t *testing.T) {
	c, err := NewCipher(testKey)
	if err != nil {
		t.Fatal(err)
	}
	c.rand = bytes.NewReader([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15})
	got, err := c.Encrypt("AKID示例<>&")
	if err != nil {
		t.Fatal(err)
	}
	if got != pythonAESVector {
		t.Fatalf("got %s want %s", got, pythonAESVector)
	}
}

func TestEncryptRoundTripUsesRandomIV(t *testing.T) {
	c, err := NewCipher(testKey)
	if err != nil {
		t.Fatal(err)
	}
	first, err := c.Encrypt("secret")
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Encrypt("secret")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("two encryptions returned identical ciphertext; IV is not random")
	}
	got, err := c.Decrypt(first)
	if err != nil || got != "secret" {
		t.Fatalf("round trip = %q, %v", got, err)
	}
}

func TestCipherRejectsBadKeyAndPadding(t *testing.T) {
	if _, err := NewCipher("short"); err == nil {
		t.Fatal("expected key length error")
	}
	c, _ := NewCipher(testKey)
	// IV only, no ciphertext block.
	if _, err := c.Decrypt("AAECAwQFBgcICQoLDA0ODw=="); err == nil {
		t.Fatal("expected short ciphertext error")
	}
}
