// Package secrets seals/opens small secrets (asset SSH credentials). v0.1 uses
// a local AES-256-GCM key as a placeholder for a real KMS envelope
// (docs/07 §7.10); v0.3 swaps in Vault/cloud KMS behind the same Sealer.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// Sealer performs authenticated encryption of secrets.
type Sealer struct {
	gcm   cipher.AEAD
	keyID string
}

// NewSealer builds a Sealer from a 16/24/32-byte key.
func NewSealer(key []byte, keyID string) (*Sealer, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secrets: cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secrets: gcm: %w", err)
	}
	return &Sealer{gcm: gcm, keyID: keyID}, nil
}

// KeyID identifies the key used (stored alongside ciphertext for rotation).
func (s *Sealer) KeyID() string { return s.keyID }

// Seal returns nonce-prefixed ciphertext.
func (s *Sealer) Seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, s.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("secrets: nonce: %w", err)
	}
	return s.gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Open reverses Seal.
func (s *Sealer) Open(ciphertext []byte) ([]byte, error) {
	ns := s.gcm.NonceSize()
	if len(ciphertext) < ns {
		return nil, errors.New("secrets: ciphertext too short")
	}
	pt, err := s.gcm.Open(nil, ciphertext[:ns], ciphertext[ns:], nil)
	if err != nil {
		return nil, fmt.Errorf("secrets: open: %w", err)
	}
	return pt, nil
}

// KeyFromString accepts a base64-encoded 16/24/32-byte key, or derives a
// 32-byte key by SHA-256 of any other string (dev convenience).
func KeyFromString(v string) ([]byte, error) {
	if v == "" {
		return nil, errors.New("secrets: empty key material")
	}
	if b, err := base64.StdEncoding.DecodeString(v); err == nil {
		switch len(b) {
		case 16, 24, 32:
			return b, nil
		}
	}
	sum := sha256.Sum256([]byte(v))
	return sum[:], nil
}
