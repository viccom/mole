package core

import (
	"encoding/json"
	"testing"
)

func TestNodeStatus(t *testing.T) {
	if NodeStatusOnline != "online" {
		t.Errorf("expected 'online', got '%s'", NodeStatusOnline)
	}
	if NodeStatusOffline != "offline" {
		t.Errorf("expected 'offline', got '%s'", NodeStatusOffline)
	}
}

func TestTunnelType(t *testing.T) {
	tests := []struct {
		tp   TunnelType
		want string
	}{
		{TunnelTypeHTTP, "http"},
		{TunnelTypeTCP, "tcp"},
		{TunnelTypeUDP, "udp"},
	}
	for _, tt := range tests {
		if string(tt.tp) != tt.want {
			t.Errorf("expected '%s', got '%s'", tt.want, string(tt.tp))
		}
	}
}

func TestApiResponse(t *testing.T) {
	resp := ApiResponse{Code: 0, Msg: "success", Data: "test"}
	if resp.Code != 0 {
		t.Error("expected code 0")
	}
	if resp.Msg != "success" {
		t.Error("expected msg 'success'")
	}
}

func TestPaginatedResponse(t *testing.T) {
	resp := PaginatedResponse{
		Items:      []string{"a", "b"},
		Total:      2,
		Page:       1,
		PerPage:    20,
		TotalPages: 1,
	}
	if resp.Total != 2 {
		t.Error("expected total 2")
	}
}

func TestPermission(t *testing.T) {
	p := Permission{Resource: "users", Action: "read"}
	if p.Resource != "users" || p.Action != "read" {
		t.Error("permission fields mismatch")
	}
}

func TestClaims(t *testing.T) {
	c := Claims{UserID: "1", Username: "admin", Roles: []string{"admin"}}
	if c.UserID != "1" || len(c.Roles) != 1 {
		t.Error("claims fields mismatch")
	}
}

func ptrBool(b bool) *bool { return &b }

func TestTunnel_IsEnabled(t *testing.T) {
	tests := []struct {
		name string
		tun  Tunnel
		want bool
	}{
		{"nil means enabled", Tunnel{Enabled: nil}, true},
		{"true means enabled", Tunnel{Enabled: ptrBool(true)}, true},
		{"false means disabled", Tunnel{Enabled: ptrBool(false)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.tun.IsEnabled(); got != tt.want {
				t.Errorf("Tunnel.IsEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAccessTokenStatus_Constants(t *testing.T) {
	if AccessTokenActive != "active" {
		t.Errorf("AccessTokenActive = %q, want %q", AccessTokenActive, "active")
	}
	if AccessTokenDisabled != "disabled" {
		t.Errorf("AccessTokenDisabled = %q, want %q", AccessTokenDisabled, "disabled")
	}
}

func TestAccessToken_JSON_Serialization(t *testing.T) {
	tok := AccessToken{
		ID:          "atk_123",
		UserID:      "user_1",
		Name:        "test token",
		TokenPrefix: "atk_",
		TokenHash:   "super-secret-hash-value",
		Status:      AccessTokenActive,
	}
	data, err := json.Marshal(tok)
	if err != nil {
		t.Fatalf("json.Marshal AccessToken: %v", err)
	}
	s := string(data)
	// TokenHash 使用 json:"token_hash" 标签，会被序列化（存储需要）
	// API 层通过手动构建响应结构体来排除它
	if !jsonContains(s, "token_hash") {
		t.Error("JSON output should contain 'token_hash' (needed for storage serialization)")
	}
	if !jsonContains(s, "super-secret-hash-value") {
		t.Error("JSON output should contain the hash value")
	}
}

// jsonContains is a helper to check if a JSON string contains a substring.
func jsonContains(s, substr string) bool {
	return len(s) >= len(substr) && containsString(s, substr)
}

func containsString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestNodeAccessGrant_Defaults(t *testing.T) {
	grant := NodeAccessGrant{}
	if grant.LegacyGlobal != false {
		t.Errorf("NodeAccessGrant{}.LegacyGlobal = %v, want false", grant.LegacyGlobal)
	}
}
