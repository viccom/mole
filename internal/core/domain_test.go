package core

import (
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
