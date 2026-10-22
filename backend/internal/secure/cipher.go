// Package secure seals the secrets the site keeps beside an alert channel.
package secure

import (
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

const keyInfo = "downradar app secrets v1"

// Cipher encrypts one value with a key derived from the install's master secret.
type Cipher struct {
	aead cipher.AEAD
}

// New derives a key from master with HKDF, so one environment value covers the
// install and the key itself never has to be stored beside the data.
func New(master string) (*Cipher, error) {
	if master == "" {
		return nil, errors.New("secure: the master key is empty")
	}

	key := make([]byte, chacha20poly1305.KeySize)
	if _, err := io.ReadFull(hkdf.New(sha256.New, []byte(master), nil, []byte(keyInfo)), key); err != nil {
		return nil, fmt.Errorf("secure: derive the key: %w", err)
	}

	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, fmt.Errorf("secure: make the cipher: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Seal encrypts one value and returns the nonce and ciphertext as base64.
func (c *Cipher) Seal(plain []byte) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("secure: make a nonce: %w", err)
	}
	sealed := c.aead.Seal(nil, nonce, plain, nil)
	return base64.StdEncoding.EncodeToString(append(nonce, sealed...)), nil
}

// Open decrypts one value. A wrong key or a changed value fails rather than
// returning something the caller could mistake for an empty setting.
func (c *Cipher) Open(encoded string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("secure: decode the value: %w", err)
	}
	if len(raw) < c.aead.NonceSize() {
		return nil, errors.New("secure: the value is too short")
	}

	nonce, sealed := raw[:c.aead.NonceSize()], raw[c.aead.NonceSize():]
	plain, err := c.aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, fmt.Errorf("secure: open the value: %w", err)
	}
	return plain, nil
}
