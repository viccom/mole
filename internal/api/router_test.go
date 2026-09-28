package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nalgeon/redka"
	_ "modernc.org/sqlite"

	"moleAgent_Serv/internal/auth"
	"moleAgent_Serv/internal/core"
)

// stubRoleRepo 空角色仓库：AccessKey 旁路测试不依赖任何角色数据
type stubRoleRepo struct{}

func (s *stubRoleRepo) Create(role *core.Role) error { return nil }
func (s *stubRoleRepo) GetByID(id string) (*core.Role, error) {
	return nil, core.ErrRoleNotFound
}
func (s *stubRoleRepo) GetAll() ([]*core.Role, error) { return nil, nil }
func (s *stubRoleRepo) Update(role *core.Role) error  { return nil }
func (s *stubRoleRepo) Delete(id string) error        { return nil }

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

// RegisterStrict 路由必须拒绝 AccessKey 旁路（SEC-06）：高危操作
// （删用户/AccessKey 管理/自更新）不允许以共享密钥身份执行
func TestRouterRegisterStrict_RejectsAccessKey(t *testing.T) {
	db, err := redka.Open(":memory:", &redka.Options{DriverName: "sqlite"})
	if err != nil {
		t.Fatalf("failed to open in-memory redka: %v", err)
	}
	defer db.Close()

	roleRepo := &stubRoleRepo{}
	mw := auth.NewAuthMiddleware(auth.NewJWTManager("test-secret", time.Hour), auth.NewRBACEngine(db, roleRepo), func() string { return "global-key-123" })

	router := NewRouter(mw, nil)
	router.RegisterStrict("DELETE", "/api/v1/users/", func(w http.ResponseWriter, r *http.Request) {
		ResponseOK(w, "deleted")
	}, "users", "delete")

	handler := router.Build()
	req := httptest.NewRequest("DELETE", "/api/v1/users/someone", nil)
	req.Header.Set("W-Access-Key", "global-key-123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for access key on strict route, got %d, body=%s", w.Code, w.Body.String())
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
