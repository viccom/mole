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

	// 空 secret 应该自动生成
	cfg.Auth.JWTSecret = ""
	err := cfg.validate()
	if err != nil {
		t.Errorf("should auto-generate for empty jwt_secret, got %v", err)
	}
	if len(cfg.Auth.JWTSecret) < 16 {
		t.Errorf("auto-generated secret too short: %d chars", len(cfg.Auth.JWTSecret))
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
	t.Setenv("MA_ADMIN_USER", "")
	t.Setenv("MA_ADMIN_PASS", "")

	u, p := AdminUser()
	if u != "admin" {
		t.Errorf("expected admin, got %s", u)
	}
	// SEC-05：MA_ADMIN_PASS 未设时不得回落公开默认口令 "admin"，
	// 改为随机强口令（≥24 字符，含大小写与数字）
	if p == "admin" || p == "" {
		t.Fatalf("must not fall back to the well-known default password, got %q", p)
	}
	if len(p) < 24 {
		t.Fatalf("random admin password must be at least 24 chars, got %d", len(p))
	}
	var hasUpper, hasLower, hasDigit bool
	for _, c := range p {
		switch {
		case c >= 'A' && c <= 'Z':
			hasUpper = true
		case c >= 'a' && c <= 'z':
			hasLower = true
		case c >= '0' && c <= '9':
			hasDigit = true
		}
	}
	if !hasUpper || !hasLower || !hasDigit {
		t.Fatalf("random admin password must contain upper/lower/digit, got %q", p)
	}
	// 同一启动周期内稳定（种子与修复路径必须落同一个哈希）
	_, p2 := AdminUser()
	if p != p2 {
		t.Fatalf("random admin password must be stable within one process, got %q then %q", p, p2)
	}

	t.Setenv("MA_ADMIN_USER", "superuser")
	t.Setenv("MA_ADMIN_PASS", "superpass")

	u, p = AdminUser()
	if u != "superuser" || p != "superpass" {
		t.Errorf("expected superuser/superpass, got %s/%s", u, p)
	}
}

// SEC-05：区分「用户显式指定口令」与「需要随机生成」
func TestAdminPassConfigured(t *testing.T) {
	t.Setenv("MA_ADMIN_PASS", "")
	if AdminPassConfigured() {
		t.Error("AdminPassConfigured must be false when MA_ADMIN_PASS unset")
	}
	t.Setenv("MA_ADMIN_PASS", "some-pass")
	if !AdminPassConfigured() {
		t.Error("AdminPassConfigured must be true when MA_ADMIN_PASS set")
	}
}

func TestConfigValidation_KCPConflicts(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.JWTSecret = "valid-secret-key-for-testing-12345"

	// KCP + TLS should be rejected
	cfg.Server.Transport = "kcp"
	cfg.Server.TLS.Enabled = true
	cfg.Server.TLS.CertFile = "cert"
	cfg.Server.TLS.KeyFile = "key"
	if err := cfg.validate(); err == nil {
		t.Error("should reject KCP with TLS enabled")
	}

	// KCP without TLS should pass
	cfg.Server.TLS.Enabled = false
	if err := cfg.validate(); err != nil {
		t.Errorf("KCP without TLS should pass, got %v", err)
	}
}

func TestConfigValidation_KCPFECShards(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.JWTSecret = "valid-secret-key-for-testing-12345"
	cfg.Server.Transport = "kcp"

	// Total > 255 should fail
	cfg.Server.KCP.DataShards = 200
	cfg.Server.KCP.ParityShards = 60
	if err := cfg.validate(); err == nil {
		t.Error("should reject dataShards + parityShards > 255")
	}

	// parityShards > 0 with dataShards = 0 should fail
	cfg.Server.KCP.DataShards = 0
	cfg.Server.KCP.ParityShards = 3
	if err := cfg.validate(); err == nil {
		t.Error("should reject parityShards > 0 without dataShards")
	}

	// Valid FEC config
	cfg.Server.KCP.DataShards = 10
	cfg.Server.KCP.ParityShards = 3
	if err := cfg.validate(); err != nil {
		t.Errorf("valid FEC config should pass, got %v", err)
	}

	// No FEC (both 0) should pass
	cfg.Server.KCP.DataShards = 0
	cfg.Server.KCP.ParityShards = 0
	if err := cfg.validate(); err != nil {
		t.Errorf("no FEC should pass, got %v", err)
	}
}

func TestConfigValidation_InvalidTransport(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.JWTSecret = "valid-secret-key-for-testing-12345"
	cfg.Server.Transport = "quic"
	if err := cfg.validate(); err == nil {
		t.Error("should reject unknown transport")
	}
}
