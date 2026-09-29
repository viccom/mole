//go:build p2p

package crypto

import (
	"strings"
	"testing"
)

const cs = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func TestGenerateSecureRandomString_LengthAndCharset(t *testing.T) {
	for _, n := range []int{8, 10, 16} {
		s, err := GenerateSecureRandomString(n)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if len(s) != n {
			t.Errorf("n=%d: got len %d", n, len(s))
		}
		for _, c := range s {
			if !strings.ContainsRune(cs, c) {
				t.Errorf("n=%d: char %q not in charset", n, c)
			}
		}
	}
}

func TestGenerateSecureRandomString_NotWeak(t *testing.T) {
	// GenerateSecureRandomString re-rolls weak passwords (needs letter+digit, len>=8).
	for i := 0; i < 50; i++ {
		s, _ := GenerateSecureRandomString(10)
		if IsWeakPassword(s) {
			t.Fatalf("got weak password: %q", s)
		}
	}
}

func TestGenerateSeededRandomString_Deterministic(t *testing.T) {
	a := GenerateSeededRandomString(8, 12345)
	b := GenerateSeededRandomString(8, 12345)
	if a != b {
		t.Fatalf("not deterministic: %q vs %q", a, b)
	}
	if len(a) != 8 {
		t.Errorf("len %d", len(a))
	}
}

func TestMakeSeed_ReturnsInt64(t *testing.T) {
	s1 := MakeSeed()
	s2 := MakeSeed()
	if s1 == 0 && s2 == 0 {
		t.Fatal("both seeds zero")
	}
}
