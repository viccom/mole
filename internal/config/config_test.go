package config

import (
	"os"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Server.ControlPort != ":9981" {
		t.Errorf("expected :9981, got %s", cfg.Server.ControlPort)
	}
	if cfg.Server.GatewayPort != ":9980" {
		t.Errorf("expected :9980, got %s", cfg.Server.GatewayPort)
	}
	if cfg.Server.MaxConcurrent != 50000 {
		t.Errorf("expected 50000, got %d", cfg.Server.MaxConcurrent)
	}
	if cfg.Logging.Level != "info" {
		t.Errorf("expected info, got %s", cfg.Logging.Level)
	}
	if cfg.Database.Path != "data/config.db" {
		t.Errorf("expected data/config.db, got %s", cfg.Database.Path)
	}
}

func TestConfigValidation(t *testing.T) {
	cfg := DefaultConfig()

	// 空 secret 应该失败
	cfg.Auth.JWTSecret = ""
	err := cfg.validate()
	if err == nil {
		t.Error("should fail with empty jwt_secret")
	}

	// 太短的 secret 应该失败
	cfg.Auth.JWTSecret = "short"
	err = cfg.validate()
	if err == nil {
		t.Error("should fail with short jwt_secret")
	}

	// 合法 secret
	cfg.Auth.JWTSecret = "valid-secret-key-for-testing-12345"
	err = cfg.validate()
	if err != nil {
		t.Errorf("should pass with valid secret, got %v", err)
	}
}

func TestConfigValidationInvalidLevel(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.JWTSecret = "valid-secret-key-for-testing-12345"
	cfg.Logging.Level = "invalid"
	err := cfg.validate()
	if err == nil {
		t.Error("should fail with invalid log level")
	}
}

func TestConfigValidationBcryptCost(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.JWTSecret = "valid-secret-key-for-testing-12345"

	cfg.Auth.BcryptCost = 3
	if cfg.validate() == nil {
		t.Error("should fail with bcrypt cost < 4")
	}

	cfg.Auth.BcryptCost = 32
	if cfg.validate() == nil {
		t.Error("should fail with bcrypt cost > 31")
	}

	cfg.Auth.BcryptCost = 12
	if cfg.validate() != nil {
		t.Error("should pass with bcrypt cost 12")
	}
}

func TestLoadWithEnvOverrides(t *testing.T) {
	os.Setenv("MA_JWT_SECRET", "env-secret-key-for-testing-12345")
	os.Setenv("MA_LOG_LEVEL", "debug")
	defer os.Unsetenv("MA_JWT_SECRET")
	defer os.Unsetenv("MA_LOG_LEVEL")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Auth.JWTSecret != "env-secret-key-for-testing-12345" {
		t.Errorf("expected env secret, got %s", cfg.Auth.JWTSecret)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("expected debug, got %s", cfg.Logging.Level)
	}
}

func TestAdminUser(t *testing.T) {
	u, p := AdminUser()
	if u != "admin" || p != "admin" {
		t.Errorf("expected admin/admin, got %s/%s", u, p)
	}

	os.Setenv("MA_ADMIN_USER", "superuser")
	os.Setenv("MA_ADMIN_PASS", "superpass")
	defer os.Unsetenv("MA_ADMIN_USER")
	defer os.Unsetenv("MA_ADMIN_PASS")

	u, p = AdminUser()
	if u != "superuser" || p != "superpass" {
		t.Errorf("expected superuser/superpass, got %s/%s", u, p)
	}
}

func TestParseExpiry(t *testing.T) {
	// 这在 main.go 中，但可以测试 time.ParseDuration
	// 简单验证
}
