package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/hkdf"
	"crypto/sha256"
)

const encPrefix = "enc:"

// SecretEncryptor provides AES-256-GCM encryption/decryption for sensitive fields.
type SecretEncryptor struct {
	aead cipher.AEAD
}

// NewSecretEncryptor derives an AES-256 key from the given master secret via HKDF.
func NewSecretEncryptor(masterSecret string) (*SecretEncryptor, error) {
	if masterSecret == "" {
		return nil, fmt.Errorf("master secret is empty")
	}
	// Derive 32-byte key using HKDF-SHA256
	salt := []byte("moleAgent-tunnel-secret-v1")
	info := []byte("aes-256-gcm-key")
	key := make([]byte, 32)
	r := hkdf.New(sha256.New, []byte(masterSecret), salt, info)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, fmt.Errorf("hkdf derive key: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	return &SecretEncryptor{aead: aead}, nil
}

// Encrypt encrypts plaintext and returns "enc:<base64>" string.
func (e *SecretEncryptor) Encrypt(plaintext string) string {
	if plaintext == "" {
		return ""
	}
	nonce := make([]byte, e.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		// Should never happen
		panic("crypto/rand failed: " + err.Error())
	}
	ciphertext := e.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return encPrefix + base64.StdEncoding.EncodeToString(ciphertext)
}

// Decrypt decrypts an "enc:<base64>" string. If the input is not encrypted
// (no enc: prefix), it is returned as-is for backward compatibility.
func (e *SecretEncryptor) Decrypt(encoded string) string {
	if encoded == "" {
		return ""
	}
	if !strings.HasPrefix(encoded, encPrefix) {
		return encoded // not encrypted, return as-is
	}
	raw, err := base64.StdEncoding.DecodeString(encoded[len(encPrefix):])
	if err != nil {
		return encoded // invalid base64, return as-is
	}
	nonceSize := e.aead.NonceSize()
	if len(raw) < nonceSize {
		return encoded // too short, return as-is
	}
	nonce, ciphertext := raw[:nonceSize], raw[nonceSize:]
	plaintext, err := e.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return encoded // decryption failed, return as-is
	}
	return string(plaintext)
}

// IsEncrypted returns true if the string has the encryption prefix.
func IsEncrypted(s string) bool {
	return strings.HasPrefix(s, encPrefix)
}
