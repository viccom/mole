package feishu

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientEnabled(t *testing.T) {
	c := NewClient("", "")
	if c.Enabled() {
		t.Error("should be disabled with empty credentials")
	}

	c = NewClient("cli_test", "secret")
	if !c.Enabled() {
		t.Error("should be enabled with credentials")
	}
}

func TestClientGetTenantAccessToken(t *testing.T) {
	var requestPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath = r.URL.Path
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"code":                0,
			"msg":                 "ok",
			"tenant_access_token": "test-tenant-token",
			"expire":              7200,
		})
	}))
	defer srv.Close()

	c := NewClient("cli_test", "secret")
	c.httpClient = srv.Client()

	token, err := c.getTenantTokenFromURL(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "test-tenant-token" {
		t.Errorf("expected test-tenant-token, got %s", token)
	}
	if requestPath != "/" {
		t.Errorf("expected /, got %s", requestPath)
	}
}

func TestClientGetTenantTokenError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"code": 99991,
			"msg":  "invalid app_id",
		})
	}))
	defer srv.Close()

	c := NewClient("bad_id", "bad_secret")
	c.httpClient = srv.Client()

	_, err := c.getTenantTokenFromURL(context.Background(), srv.URL)
	if err == nil {
		t.Error("should return error for API error response")
	}
}

func TestClientGetUserAccessToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-tenant-token" {
			w.WriteHeader(401)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"msg":  "ok",
			"data": map[string]string{
				"access_token": "test-user-token",
			},
		})
	}))
	defer srv.Close()

	c := NewClient("cli_test", "secret")
	c.httpClient = srv.Client()

	token, err := c.getUserAccessTokenFromURL(context.Background(), "auth-code-123", "test-tenant-token", srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "test-user-token" {
		t.Errorf("expected test-user-token, got %s", token)
	}
}

func TestClientGetUserInfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-user-token" {
			w.WriteHeader(401)
			return
		}
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"msg":  "ok",
			"data": map[string]string{
				"open_id":    "ou_abc123",
				"name":       "Zhang San",
				"avatar_url": "https://img.example.com/avatar.png",
			},
		})
	}))
	defer srv.Close()

	c := NewClient("cli_test", "secret")
	c.httpClient = srv.Client()

	info, err := c.getUserInfoFromURL(context.Background(), "test-user-token", srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.OpenID != "ou_abc123" {
		t.Errorf("expected ou_abc123, got %s", info.OpenID)
	}
	if info.Name != "Zhang San" {
		t.Errorf("expected Zhang San, got %s", info.Name)
	}
	if info.AvatarURL != "https://img.example.com/avatar.png" {
		t.Errorf("unexpected avatar_url: %s", info.AvatarURL)
	}
}
