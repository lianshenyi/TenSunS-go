// Package cryptox implements the two wire-compatible crypto formats shared
// with the Python implementation: AES-256-CBC/PKCS#7 for secrets and
// itsdangerous' timed JWS for login tokens.
package cryptox

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Cipher performs compatibility encryption using a raw 32-byte UTF-8 key.
type Cipher struct {
	key  []byte
	rand io.Reader
}

// NewCipher validates and wraps the skey value read from Consul.
func NewCipher(key string) (*Cipher, error) {
	if len([]byte(key)) != 32 {
		return nil, fmt.Errorf("AES key must be exactly 32 UTF-8 bytes, got %d", len([]byte(key)))
	}
	return &Cipher{key: []byte(key), rand: rand.Reader}, nil
}

// Encrypt returns standard Base64 of IV || AES-256-CBC(PKCS#7(plaintext)).
func (c *Cipher) Encrypt(plaintext string) (string, error) {
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return "", err
	}
	iv := make([]byte, aes.BlockSize)
	if _, err := io.ReadFull(c.rand, iv); err != nil {
		return "", fmt.Errorf("generate IV: %w", err)
	}
	padded := pkcs7Pad([]byte(plaintext), aes.BlockSize)
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, padded)
	out := make([]byte, 0, len(iv)+len(ct))
	out = append(out, iv...)
	out = append(out, ct...)
	return base64.StdEncoding.EncodeToString(out), nil
}

// Decrypt accepts the same format produced by flask-consul/units/myaes.py.
func (c *Cipher) Decrypt(encoded string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}
	if len(raw) < 2*aes.BlockSize {
		return "", errors.New("ciphertext is too short")
	}
	iv, ct := raw[:aes.BlockSize], raw[aes.BlockSize:]
	if len(ct)%aes.BlockSize != 0 {
		return "", errors.New("ciphertext is not block aligned")
	}
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return "", err
	}
	plain := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ct)
	plain, err = pkcs7Unpad(plain, aes.BlockSize)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	n := blockSize - len(data)%blockSize
	return append(data, bytes.Repeat([]byte{byte(n)}, n)...)
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, errors.New("invalid PKCS#7 data length")
	}
	n := int(data[len(data)-1])
	if n == 0 || n > blockSize || n > len(data) {
		return nil, errors.New("invalid PKCS#7 padding")
	}
	for _, b := range data[len(data)-n:] {
		if int(b) != n {
			return nil, errors.New("invalid PKCS#7 padding bytes")
		}
	}
	return data[:len(data)-n], nil
}
