//go:build p2p

package crypto

import (
	"crypto/sha256"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

// KeyLen is the AES-256 key length in bytes.
const KeyLen = 32

// DeriveKey returns a 32-byte key derived from secret and a context string
// via HKDF-SHA256 with a domain-separated salt. Used to derive per-channel
// keys from a shared secret (e.g. ECDHE shared secret → transport key).
func DeriveKey(secret string, ctx string) (*[KeyLen]byte, error) {
	salt := fmt.Sprintf("p2punch-%s", ctx)
	r := hkdf.New(sha256.New, []byte(secret), []byte(salt), []byte(ctx))
	var key [KeyLen]byte
	if _, err := io.ReadFull(r, key[:]); err != nil {
		return nil, fmt.Errorf("crypto: hkdf: %w", err)
	}
	return &key, nil
}
