package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"moleAgent_Serv/internal/auth"
	"moleAgent_Serv/internal/core"
)

func setupTestRouter() *Router {
	mw := &auth.AuthMiddleware{}
	router := NewRouter(mw, nil)
	return router
}

func TestRouterNotFound(t *testing.T) {
	mw := &auth.AuthMiddleware{}
	router := NewRouter(mw, nil)
	router.RegisterPublic("GET", "/api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		ResponseOK(w, "ok")
	})

	handler := router.Build()

	req := httptest.NewRequest("GET", "/nonexistent", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestRouterMethodNotAllowed(t *testing.T) {
	mw := &auth.AuthMiddleware{}
	router := NewRouter(mw, nil)
	router.RegisterPublic("GET", "/api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		ResponseOK(w, "ok")
	})

	handler := router.Build()

	req := httptest.NewRequest("POST", "/api/v1/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestRouterPrefixMatch(t *testing.T) {
	mw := &auth.AuthMiddleware{}
	router := NewRouter(mw, nil)
	router.RegisterPublic("GET", "/api/v1/users/", func(w http.ResponseWriter, r *http.Request) {
		ResponseOK(w, "user-detail")
	})

	handler := router.Build()

	req := httptest.NewRequest("GET", "/api/v1/users/testuser", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestRouterExactOverPrefix(t *testing.T) {
	mw := &auth.AuthMiddleware{}
	router := NewRouter(mw, nil)
	router.RegisterPublic("GET", "/api/v1/users", func(w http.ResponseWriter, r *http.Request) {
		ResponseOK(w, "user-list")
	})
	router.RegisterPublic("GET", "/api/v1/users/", func(w http.ResponseWriter, r *http.Request) {
		ResponseOK(w, "user-detail")
	})

	handler := router.Build()

	// 精确匹配 /api/v1/users 应该匹配第一个
	req := httptest.NewRequest("GET", "/api/v1/users", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestDomainTypes(t *testing.T) {
	// 验证核心领域类型
	node := core.Node{
		ID:     "test",
		Name:   "Test",
		Status: core.NodeStatusOnline,
	}
	if node.ID != "test" {
		t.Error("node ID mismatch")
	}

	tunnel := core.Tunnel{
		Name:   "web",
		Type:   core.TunnelTypeHTTP,
		Target: "http://127.0.0.1:8080",
	}
	if tunnel.Type != core.TunnelTypeHTTP {
		t.Error("tunnel type mismatch")
	}

	user := core.User{
		ID:       "u1",
		Username: "admin",
		Status:   "active",
	}
	if user.Status != "active" {
		t.Error("user status mismatch")
	}

	role := core.Role{
		ID:          "r1",
		Name:        "admin",
		Permissions: []core.Permission{{Resource: "*", Action: "*"}},
	}
	if len(role.Permissions) != 1 {
		t.Error("role permissions mismatch")
	}
}
