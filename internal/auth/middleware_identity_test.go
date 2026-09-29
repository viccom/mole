package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nalgeon/redka"
	_ "modernc.org/sqlite"

	"moleAgent_Serv/internal/core"
)

func TestRequirePermission_UsesUserIDWhenUsernameDiffers(t *testing.T) {
	db, err := redka.Open(":memory:", &redka.Options{DriverName: "sqlite"})
	if err != nil {
		t.Fatalf("failed to open in-memory redka: %v", err)
	}
	defer db.Close()

	roleRepo := &mockRoleRepo{
		roles: map[string]*core.Role{
			"operator": {
				ID:   "operator",
				Name: "operator",
				Permissions: []core.Permission{
					{Resource: "mqtt", Action: "read"},
				},
			},
		},
	}

	userID := "uuid-op-003"
	if _, err := db.Hash().Set("user_roles", userID, `["operator"]`); err != nil {
		t.Fatalf("failed to set user_roles: %v", err)
	}

	jwtMgr := NewJWTManager("test-secret", time.Hour)
	token, _, err := jwtMgr.GenerateToken(&core.Claims{
		UserID:   userID,
		Username: "operator-display-name",
	})
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}

	rbac := NewRBACEngine(db, roleRepo)
	mw := NewAuthMiddleware(jwtMgr, rbac, func() string { return "" })

	called := false
	handler := mw.RequirePermission("mqtt", "read")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/mqtt", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Fatal("expected protected handler to be called")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// ===== SEC-06：AccessKey 旁路收窄 =====

// newAccessKeyTestMiddleware 构造带全局 AccessKey 与 admin 角色用户的测试中间件
func newAccessKeyTestMiddleware(t *testing.T) (*AuthMiddleware, string) {
	t.Helper()
	db, err := redka.Open(":memory:", &redka.Options{DriverName: "sqlite"})
	if err != nil {
		t.Fatalf("failed to open in-memory redka: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	roleRepo := &mockRoleRepo{
		roles: map[string]*core.Role{
			"admin": {
				ID:          "admin",
				Name:        "admin",
				Permissions: []core.Permission{{Resource: "*", Action: "*"}},
			},
		},
	}
	if _, err := db.Hash().Set("user_roles", "uuid-admin-001", `["admin"]`); err != nil {
		t.Fatalf("failed to set user_roles: %v", err)
	}
	rbac := NewRBACEngine(db, roleRepo)
	jwtMgr := NewJWTManager("test-secret", time.Hour)
	mw := NewAuthMiddleware(jwtMgr, rbac, func() string { return "global-key-123" })
	return mw, "uuid-admin-001"
}

// 高危路由的 strict 变体必须拒绝 AccessKey 旁路身份（SEC-06）
func TestRequirePermissionNoAccessKey_RejectsAccessKey(t *testing.T) {
	mw, _ := newAccessKeyTestMiddleware(t)

	called := false
	handler := mw.RequirePermissionNoAccessKey("users", "delete")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/users/someone", nil)
	req.Header.Set("W-Access-Key", "global-key-123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if called {
		t.Fatal("strict middleware must not call handler for access_key identity")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for access key on strict route, got %d", w.Code)
	}
}

// JWT 用户在 strict 路由上仍走正常 RBAC：有权限则放行
func TestRequirePermissionNoAccessKey_JWTUserWithPermissionPasses(t *testing.T) {
	mw, adminID := newAccessKeyTestMiddleware(t)
	token, _, err := mw.jwtMgr.GenerateToken(&core.Claims{UserID: adminID, Username: "admin"})
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}

	called := false
	handler := mw.RequirePermissionNoAccessKey("users", "delete")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/users/someone", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Fatal("expected protected handler to be called for JWT admin")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// 收窄只作用于 strict 变体：普通权限路由保留 AccessKey 旁路（现有行为）
func TestRequirePermission_AccessKeyStillBypassesOrdinaryRoutes(t *testing.T) {
	mw, _ := newAccessKeyTestMiddleware(t)

	called := false
	handler := mw.RequirePermission("nodes", "read")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/nodes", nil)
	req.Header.Set("W-Access-Key", "global-key-123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Fatal("ordinary route must keep access key bypass")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}
