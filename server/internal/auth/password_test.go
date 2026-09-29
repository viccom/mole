package auth

import (
	"errors"
	"testing"

	"moleAgent_Serv/internal/core"
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

// SEC-14：口令强度校验是唯一入口，Create/Update/resetPassword/ChangePassword 共用
func TestValidatePasswordStrength(t *testing.T) {
	if err := ValidatePasswordStrength("abc"); !errors.Is(err, core.ErrPasswordTooShort) {
		t.Errorf("expected ErrPasswordTooShort for 3-char password, got %v", err)
	}
	if err := ValidatePasswordStrength("1234567"); !errors.Is(err, core.ErrPasswordTooShort) {
		t.Errorf("expected ErrPasswordTooShort for 7-char password, got %v", err)
	}
	if err := ValidatePasswordStrength("12345678"); err != nil {
		t.Errorf("expected nil for 8-char password, got %v", err)
	}
	if err := ValidatePasswordStrength("much-longer-password-123"); err != nil {
		t.Errorf("expected nil for long password, got %v", err)
	}
}
