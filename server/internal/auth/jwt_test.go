package auth

import (
	"testing"
	"time"

	"moleAgent_Serv/internal/core"
)

func TestJWTManager(t *testing.T) {
	jm := NewJWTManager("test-secret-key-for-unit-testing-12345", time.Hour)

	claims := &core.Claims{
		UserID:   "user1",
		Username: "testuser",
		Roles:    []string{"admin"},
	}

	// Generate
	token, exp, err := jm.GenerateToken(claims)
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}
	if token == "" {
		t.Error("token should not be empty")
	}
	if exp.IsZero() {
		t.Error("expiry should not be zero")
	}

	// Verify
	got, err := jm.VerifyToken(token)
	if err != nil {
		t.Fatalf("VerifyToken failed: %v", err)
	}
	if got.UserID != "user1" {
		t.Errorf("expected user1, got %s", got.UserID)
	}
	if got.Username != "testuser" {
		t.Errorf("expected testuser, got %s", got.Username)
	}
}

func TestJWTManagerInvalidToken(t *testing.T) {
	jm := NewJWTManager("test-secret-key-for-unit-testing-12345", time.Hour)

	_, err := jm.VerifyToken("invalid.token.here")
	if err != core.ErrTokenInvalid {
		t.Errorf("expected ErrTokenInvalid, got %v", err)
	}
}

func TestJWTManagerWrongSecret(t *testing.T) {
	jm1 := NewJWTManager("secret-1-with-enough-length-for-test-1234", time.Hour)
	jm2 := NewJWTManager("secret-2-with-enough-length-for-test-5678", time.Hour)

	claims := &core.Claims{UserID: "user1", Username: "test"}
	token, _, _ := jm1.GenerateToken(claims)

	_, err := jm2.VerifyToken(token)
	if err != core.ErrTokenInvalid {
		t.Errorf("should fail with wrong secret, got %v", err)
	}
}

func TestJWTManagerExpiredToken(t *testing.T) {
	jm := NewJWTManager("test-secret-key-for-unit-testing-12345", -time.Hour)

	claims := &core.Claims{UserID: "user1", Username: "test"}
	token, _, _ := jm.GenerateToken(claims)

	_, err := jm.VerifyToken(token)
	if err != core.ErrTokenExpired {
		t.Errorf("expected ErrTokenExpired, got %v", err)
	}
}
