//go:build p2p

package crypto

import (
	"testing"
)

func TestECDHE_BothSidesAgree(t *testing.T) {
	alice, err := GenerateECDHEKey()
	if err != nil {
		t.Fatal(err)
	}
	bob, err := GenerateECDHEKey()
	if err != nil {
		t.Fatal(err)
	}
	alicePub, err := FromPublicKeyBytes(alice.PublicKeyBytes())
	if err != nil {
		t.Fatal(err)
	}
	bobPub, err := FromPublicKeyBytes(bob.PublicKeyBytes())
	if err != nil {
		t.Fatal(err)
	}
	aliceShared, err := alice.DeriveSharedKey(bobPub)
	if err != nil {
		t.Fatal(err)
	}
	bobShared, err := bob.DeriveSharedKey(alicePub)
	if err != nil {
		t.Fatal(err)
	}
	if *aliceShared != *bobShared {
		t.Fatal("ECDHE: alice and bob derived different shared keys")
	}
}

func TestECDHE_DifferentKeypairsDiverge(t *testing.T) {
	a1, _ := GenerateECDHEKey()
	a2, _ := GenerateECDHEKey()
	bob, _ := GenerateECDHEKey()
	bobPub, _ := FromPublicKeyBytes(bob.PublicKeyBytes())
	s1, _ := a1.DeriveSharedKey(bobPub)
	s2, _ := a2.DeriveSharedKey(bobPub)
	if *s1 == *s2 {
		t.Fatal("different alice keypairs produced same shared key")
	}
}

func TestECDHE_PublicKeyFormat(t *testing.T) {
	k, _ := GenerateECDHEKey()
	pub := k.PublicKeyBytes()
	if len(pub) != 65 {
		t.Errorf("pub key len %d, want 65 (uncompressed P-256)", len(pub))
	}
	if pub[0] != 0x04 {
		t.Errorf("first byte 0x%02x, want 0x04 (uncompressed)", pub[0])
	}
}
