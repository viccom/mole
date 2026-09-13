//go:build p2p

package crypto

import (
	"testing"
)

func TestDeriveKey_Length(t *testing.T) {
	k, err := DeriveKey("secret", "ctx")
	if err != nil {
		t.Fatal(err)
	}
	if len(k) != KeyLen {
		t.Errorf("len %d, want %d", len(k), KeyLen)
	}
}

func TestDeriveKey_Deterministic(t *testing.T) {
	a, _ := DeriveKey("secret", "ctx")
	b, _ := DeriveKey("secret", "ctx")
	if *a != *b {
		t.Fatal("not deterministic")
	}
}

func TestDeriveKey_DifferentContextDiverges(t *testing.T) {
	a, _ := DeriveKey("secret", "ctx-a")
	b, _ := DeriveKey("secret", "ctx-b")
	if *a == *b {
		t.Fatal("different context produced same key")
	}
}
