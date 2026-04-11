package auth

import (
	"testing"
)

func TestHashAndVerifyPassword(t *testing.T) {
	password := "TestPassword123"
	hash, err := HashPassword(password, 4) // 低 cost 加快速度
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}
	if hash == "" {
		t.Error("hash should not be empty")
	}
	if hash == password {
		t.Error("hash should not equal original password")
	}

	// Verify correct
	if !VerifyPassword(password, hash) {
		t.Error("VerifyPassword should return true for correct password")
	}

	// Verify incorrect
	if VerifyPassword("wrong", hash) {
		t.Error("VerifyPassword should return false for wrong password")
	}
}

func TestHashPasswordDifferentSalts(t *testing.T) {
	password := "SamePassword123"
	hash1, _ := HashPassword(password, 4)
	hash2, _ := HashPassword(password, 4)

	if hash1 == hash2 {
		t.Error("two hashes of same password should differ (different salts)")
	}

	// Both should verify
	if !VerifyPassword(password, hash1) || !VerifyPassword(password, hash2) {
		t.Error("both hashes should verify against original password")
	}
}
