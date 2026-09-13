//go:build p2p

package crypto

import (
	"crypto/ecdh"
	"crypto/rand"
	"fmt"
)

// ECDHEKey is a P-256 ephemeral keypair for ECDH key agreement.
// Used to derive a forward-secret transport key during MQTT address exchange.
type ECDHEKey struct {
	priv *ecdh.PrivateKey
}

// GenerateECDHEKey generates a new P-256 keypair.
func GenerateECDHEKey() (*ECDHEKey, error) {
	curve := ecdh.P256()
	priv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("crypto: ecdhe generate: %w", err)
	}
	return &ECDHEKey{priv: priv}, nil
}

// PublicKeyBytes returns the uncompressed P-256 public key (65 bytes: 0x04 || X || Y),
// suitable for transmitting over the MQTT exchange payload.
func (k *ECDHEKey) PublicKeyBytes() []byte {
	return k.priv.PublicKey().Bytes()
}

// FromPublicKeyBytes parses a peer's uncompressed P-256 public key.
func FromPublicKeyBytes(b []byte) (*ecdh.PublicKey, error) {
	curve := ecdh.P256()
	return curve.NewPublicKey(b)
}

// DeriveSharedKey performs ECDH with the peer's public key, then HKDF-SHA256
// to produce a 32-byte session key. This is the PFS transport key.
func (k *ECDHEKey) DeriveSharedKey(peerPub *ecdh.PublicKey) (*[KeyLen]byte, error) {
	shared, err := k.priv.ECDH(peerPub)
	if err != nil {
		return nil, fmt.Errorf("crypto: ecdh: %w", err)
	}
	return DeriveKey(string(shared), "ecdhe-p256-v1")
}
