package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nalgeon/redka"
	_ "modernc.org/sqlite"

	"moleAgent_Serv/internal/auth"
	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/node"
)

// --- mock UserRepo ---

type mockUserRepo struct {
	users       map[string]*core.User
	passwords   map[string]string // userID -> hash
	deleteCalls []string
}

func newMockUserRepo() *mockUserRepo {
	return &mockUserRepo{
		users:     make(map[string]*core.User),
		passwords: make(map[string]string),
	}
}

func (m *mockUserRepo) Create(user *core.User, passwordHash string) error {
	c := *user
	m.users[user.ID] = &c
	m.passwords[user.ID] = passwordHash
	return nil
}

func (m *mockUserRepo) GetByID(id string) (*core.User, error) {
	u, ok := m.users[id]
	if !ok {
		return nil, core.ErrNotFound
	}
	c := *u
	return &c, nil
}

func (m *mockUserRepo) GetByUsername(username string) (*core.User, error) {
	for _, u := range m.users {
		if u.Username == username {
			c := *u
			return &c, nil
		}
	}
	return nil, core.ErrNotFound
}

func (m *mockUserRepo) GetAll() ([]*core.User, error) {
	var out []*core.User
	for _, u := range m.users {
		c := *u
		out = append(out, &c)
	}
	return out, nil
}

func (m *mockUserRepo) Update(user *core.User) error {
	c := *user
	m.users[user.ID] = &c
	return nil
}

func (m *mockUserRepo) Delete(id string) error {
	m.deleteCalls = append(m.deleteCalls, id)
	delete(m.users, id)
	delete(m.passwords, id)
	return nil
}

func (m *mockUserRepo) SetPasswordHash(id, hash string) error {
	m.passwords[id] = hash
	return nil
}

func (m *mockUserRepo) GetPasswordHash(id string) (string, error) {
	h, ok := m.passwords[id]
	if !ok {
		return "", core.ErrNotFound
	}
	return h, nil
}

// mapRoleRepo 按 ID 返回预置角色（权限检查测试用）
type mapRoleRepo struct {
	roles map[string]*core.Role
}

func (r *mapRoleRepo) Create(role *core.Role) error {
	c := *role
	r.roles[role.ID] = &c
	return nil
}
func (r *mapRoleRepo) GetByID(id string) (*core.Role, error) {
	role, ok := r.roles[id]
	if !ok {
		return nil, core.ErrRoleNotFound
	}
	c := *role
	return &c, nil
}
func (r *mapRoleRepo) GetAll() ([]*core.Role, error) { return nil, nil }
func (r *mapRoleRepo) Update(role *core.Role) error  { return nil }
func (r *mapRoleRepo) Delete(id string) error        { return nil }

// newResetPasswordRBAC 构造带 writer（仅 users:write）与 useradmin（users:admin）
// 两类用户的真实 RBAC 引擎（SEC-08 权限检查测试）
func newResetPasswordRBAC(t *testing.T) *auth.RBACEngine {
	t.Helper()
	db, err := redka.Open(":memory:", &redka.Options{DriverName: "sqlite"})
	if err != nil {
		t.Fatalf("failed to open in-memory redka: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	roleRepo := &mapRoleRepo{roles: map[string]*core.Role{
		"writer":    {ID: "writer", Name: "writer", Permissions: []core.Permission{{Resource: "users", Action: "write"}}},
		"useradmin": {ID: "useradmin", Name: "useradmin", Permissions: []core.Permission{{Resource: "users", Action: "admin"}}},
	}}
	rbac := auth.NewRBACEngine(db, roleRepo)
	if err := rbac.AssignRole("writer", "writer"); err != nil {
		t.Fatalf("AssignRole writer failed: %v", err)
	}
	if err := rbac.AssignRole("useradmin", "useradmin"); err != nil {
		t.Fatalf("AssignRole useradmin failed: %v", err)
	}
	return rbac
}

// SEC-08：重置密码必须持 users:admin，仅 users:write 的操作者不得改他人密码
func TestUserHandler_ResetPassword_RequiresUsersAdmin(t *testing.T) {
	userRepo := newMockUserRepo()
	userRepo.Create(&core.User{
		ID: "victim", Username: "victim", Status: core.UserStatusActive,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}, "old-hash")
	handler := NewUserHandler(userRepo, newResetPasswordRBAC(t), 10, nil, nil, nil)

	body := []byte(`{"password":"NewStrongPass123"}`)

	// 仅 users:write 的操作者 → 403
	req := reqWithClaims(http.MethodPut, "/api/v1/users/victim/password", body,
		&core.Claims{UserID: "writer", Roles: []string{"writer"}})
	w := httptest.NewRecorder()
	handler.Update(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for users:write operator, got %d, body=%s", w.Code, w.Body.String())
	}
	if userRepo.passwords["victim"] != "old-hash" {
		t.Fatal("password must not be changed without users:admin")
	}

	// 持 users:admin → 200 且密码被重置
	req = reqWithClaims(http.MethodPut, "/api/v1/users/victim/password", body,
		&core.Claims{UserID: "useradmin", Roles: []string{"useradmin"}})
	w = httptest.NewRecorder()
	handler.Update(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for users:admin operator, got %d, body=%s", w.Code, w.Body.String())
	}
	if userRepo.passwords["victim"] == "old-hash" || userRepo.passwords["victim"] == "" {
		t.Fatalf("password should be reset, got %q", userRepo.passwords["victim"])
	}
}

// 无 claims（防御分支）→ 403
func TestUserHandler_ResetPassword_NoClaims_Forbidden(t *testing.T) {
	userRepo := newMockUserRepo()
	userRepo.Create(&core.User{
		ID: "victim", Username: "victim", Status: core.UserStatusActive,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}, "old-hash")
	handler := NewUserHandler(userRepo, newResetPasswordRBAC(t), 10, nil, nil, nil)

	req := reqWithClaims(http.MethodPut, "/api/v1/users/victim/password",
		[]byte(`{"password":"NewStrongPass123"}`), nil)
	w := httptest.NewRecorder()
	handler.Update(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without claims, got %d, body=%s", w.Code, w.Body.String())
	}
}

// 路由级：未认证请求在中间件处被 401 挡下，到不了 handler
func TestUserHandler_ResetPassword_Unauthenticated_401(t *testing.T) {
	db, err := redka.Open(":memory:", &redka.Options{DriverName: "sqlite"})
	if err != nil {
		t.Fatalf("failed to open in-memory redka: %v", err)
	}
	defer db.Close()
	mw := auth.NewAuthMiddleware(auth.NewJWTManager("test-secret", time.Hour), auth.NewRBACEngine(db, &stubRoleRepo{}), func() string { return "" })

	userRepo := newMockUserRepo()
	userRepo.Create(&core.User{
		ID: "victim", Username: "victim", Status: core.UserStatusActive,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}, "old-hash")
	handler := NewUserHandler(userRepo, nil, 10, nil, nil, nil)

	router := NewRouter(mw, nil)
	router.Register("PUT", "/api/v1/users/", handler.Update, "users", "write")

	req := httptest.NewRequest(http.MethodPut, "/api/v1/users/victim/password", nil)
	w := httptest.NewRecorder()
	router.Build().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated request, got %d, body=%s", w.Code, w.Body.String())
	}
	if userRepo.passwords["victim"] != "old-hash" {
		t.Fatal("password must not be changed when unauthenticated")
	}
}

// QUA-07：状态子路径仅接受 core 定义的合法 UserStatus 常量
func TestUserHandler_SetStatus_RejectsUnknownStatus(t *testing.T) {
	userRepo := newMockUserRepo()
	userRepo.Create(&core.User{
		ID: "userA", Username: "alice", Status: core.UserStatusActive,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}, "hash")
	handler := NewUserHandler(userRepo, nil, 10, nil, nil, nil)

	// 非法状态 → 400，且不落库
	req := reqWithClaims(http.MethodPut, "/api/v1/users/userA/status",
		[]byte(`{"status":"hacked"}`), &core.Claims{UserID: "admin", Roles: []string{"admin"}})
	w := httptest.NewRecorder()
	handler.Update(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown status, got %d, body=%s", w.Code, w.Body.String())
	}
	u, _ := userRepo.GetByID("userA")
	if u.Status != core.UserStatusActive {
		t.Fatalf("status must not be persisted for invalid value, got %s", u.Status)
	}

	// 合法值 active / disabled → 200
	for _, status := range []string{"active", "disabled"} {
		req := reqWithClaims(http.MethodPut, "/api/v1/users/userA/status",
			[]byte(`{"status":"`+status+`"}`), &core.Claims{UserID: "admin", Roles: []string{"admin"}})
		w := httptest.NewRecorder()
		handler.Update(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for valid status %q, got %d, body=%s", status, w.Code, w.Body.String())
		}
		u, _ := userRepo.GetByID("userA")
		if string(u.Status) != status {
			t.Fatalf("expected status %q persisted, got %q", status, u.Status)
		}
	}
}

// QUA-07：白名单必须覆盖全部三个落库入口——Create/Update 原先直接
// core.UserStatus(req.Status) 落库，任意字符串（如 "hacked"）被原样存储回显
func TestUserHandler_Create_RejectsUnknownStatus(t *testing.T) {
	userRepo := newMockUserRepo()
	handler := NewUserHandler(userRepo, nil, 10, nil, nil, nil)

	req := reqWithClaims(http.MethodPost, "/api/v1/users",
		[]byte(`{"username":"bob","password":"Str0ngPass!x","status":"hacked"}`),
		&core.Claims{UserID: "admin", Roles: []string{"admin"}})
	w := httptest.NewRecorder()
	handler.Create(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("Create with unknown status must 400, got %d, body=%s", w.Code, w.Body.String())
	}
	if u, err := userRepo.GetByUsername("bob"); err == nil {
		t.Fatalf("invalid-status user must not be persisted, got %+v", u)
	}

	// 合法值与默认值路径不受影响
	req = reqWithClaims(http.MethodPost, "/api/v1/users",
		[]byte(`{"username":"bob","password":"Str0ngPass!x","status":"disabled"}`),
		&core.Claims{UserID: "admin", Roles: []string{"admin"}})
	w = httptest.NewRecorder()
	handler.Create(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Create with valid status must 200, got %d, body=%s", w.Code, w.Body.String())
	}
	u, _ := userRepo.GetByUsername("bob")
	if u.Status != core.UserStatusDisabled {
		t.Fatalf("expected disabled persisted, got %s", u.Status)
	}
}

func TestUserHandler_Update_RejectsUnknownStatus(t *testing.T) {
	userRepo := newMockUserRepo()
	userRepo.Create(&core.User{
		ID: "userA", Username: "alice", Status: core.UserStatusActive,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}, "hash")
	handler := NewUserHandler(userRepo, nil, 10, nil, nil, nil)

	req := reqWithClaims(http.MethodPut, "/api/v1/users/userA",
		[]byte(`{"status":"hacked"}`), &core.Claims{UserID: "admin", Roles: []string{"admin"}})
	w := httptest.NewRecorder()
	handler.Update(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("Update with unknown status must 400, got %d, body=%s", w.Code, w.Body.String())
	}
	u, _ := userRepo.GetByID("userA")
	if u.Status != core.UserStatusActive {
		t.Fatalf("invalid status must not be persisted, got %s", u.Status)
	}
}

// REL-07：级联步骤（AccessToken 禁用）失败时 Delete 返回 500，
// 不静默继续删除用户
func TestUserHandler_Delete_AccessTokenDisableFailure_500(t *testing.T) {
	userRepo := newMockUserRepo()
	userRepo.Create(&core.User{
		ID: "userA", Username: "alice", Status: core.UserStatusActive,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}, "fake-hash")
	atRepo := newMockAccessTokenRepo()
	atRepo.failUpdate = true
	atRepo.Create(newTestAccessToken("userA", "token1"))

	handler := NewUserHandler(userRepo, nil, 10, newTestNodeRepo(), atRepo, nil)
	req := reqWithClaims(http.MethodDelete, "/api/v1/users/userA", nil,
		&core.Claims{UserID: "admin", Roles: []string{"admin"}})
	w := httptest.NewRecorder()
	handler.Delete(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when access token disable fails, got %d, body=%s", w.Code, w.Body.String())
	}
	// 用户不得被删除（静默继续会留下已失效归属/凭据的半删状态）
	if len(userRepo.deleteCalls) != 0 {
		t.Fatalf("user must not be deleted on cascade failure, deleteCalls=%v", userRepo.deleteCalls)
	}
}

// --- tests ---

func TestUserHandler_Delete_DisablesAccessTokens(t *testing.T) {
	userRepo := newMockUserRepo()
	atRepo := newMockAccessTokenRepo()
	nodeRepo := newTestNodeRepo()

	// Create a user
	user := &core.User{
		ID:        "userA",
		Username:  "alice",
		Status:    core.UserStatusActive,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	userRepo.Create(user, "fake-hash")

	// Create 2 active tokens for that user
	tok1 := newTestAccessToken("userA", "token1")
	tok2 := newTestAccessToken("userA", "token2")
	atRepo.Create(tok1)
	atRepo.Create(tok2)

	handler := NewUserHandler(userRepo, nil, 10, nodeRepo, atRepo, nil)

	req := reqWithClaims(http.MethodDelete, "/api/v1/users/userA", nil,
		&core.Claims{UserID: "admin", Roles: []string{"admin"}})
	w := httptest.NewRecorder()

	handler.Delete(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}

	// Verify both tokens are disabled
	t1, _ := atRepo.GetByID(tok1.ID)
	if t1.Status != core.AccessTokenDisabled {
		t.Fatalf("expected token1 status=disabled, got %s", t1.Status)
	}
	t2, _ := atRepo.GetByID(tok2.ID)
	if t2.Status != core.AccessTokenDisabled {
		t.Fatalf("expected token2 status=disabled, got %s", t2.Status)
	}

	// Verify user is deleted
	if _, err := userRepo.GetByID("userA"); err == nil {
		t.Fatal("user should be deleted")
	}
}

func TestUserHandler_Delete_ReassignsNodesToSystem(t *testing.T) {
	userRepo := newMockUserRepo()
	atRepo := newMockAccessTokenRepo()
	nodeRepo := newTestNodeRepo()

	// Create a user
	user := &core.User{
		ID:        "userA",
		Username:  "alice",
		Status:    core.UserStatusActive,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	userRepo.Create(user, "fake-hash")

	// Create 3 nodes owned by that user
	nodes := []*core.Node{
		{ID: "Node1", Name: "n1", OwnerUserID: "userA"},
		{ID: "Node2", Name: "n2", OwnerUserID: "userA"},
		{ID: "Node3", Name: "n3", OwnerUserID: "userA"},
	}
	for _, n := range nodes {
		nodeRepo.Create(n)
	}

	// Also create a node owned by another user (should NOT be reassigned)
	nodeRepo.Create(&core.Node{ID: "NodeOther", Name: "other", OwnerUserID: "userB"})

	handler := NewUserHandler(userRepo, nil, 10, nodeRepo, atRepo, nil)

	req := reqWithClaims(http.MethodDelete, "/api/v1/users/userA", nil,
		&core.Claims{UserID: "admin", Roles: []string{"admin"}})
	w := httptest.NewRecorder()

	handler.Delete(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}

	// Verify all userA's nodes now have OwnerUserID="system"
	for _, nodeID := range []string{"Node1", "Node2", "Node3"} {
		n, err := nodeRepo.GetByID(nodeID)
		if err != nil {
			t.Fatalf("node %s should still exist: %v", nodeID, err)
		}
		if n.OwnerUserID != "system" {
			t.Fatalf("expected node %s OwnerUserID=system, got %s", nodeID, n.OwnerUserID)
		}
	}

	// Verify other user's node is unchanged
	other, err := nodeRepo.GetByID("NodeOther")
	if err != nil {
		t.Fatalf("NodeOther should still exist: %v", err)
	}
	if other.OwnerUserID != "userB" {
		t.Fatalf("NodeOther should still be owned by userB, got %s", other.OwnerUserID)
	}
}

func TestUserHandler_Delete_NilRepos_NoPanic(t *testing.T) {
	userRepo := newMockUserRepo()

	// Create a user
	user := &core.User{
		ID:        "userA",
		Username:  "alice",
		Status:    core.UserStatusActive,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	userRepo.Create(user, "fake-hash")

	// Handler with nil accessTokenRepo and nil nodeRepo
	handler := NewUserHandler(userRepo, nil, 10, nil, nil, nil)

	req := reqWithClaims(http.MethodDelete, "/api/v1/users/userA", nil,
		&core.Claims{UserID: "admin", Roles: []string{"admin"}})
	w := httptest.NewRecorder()

	// Should not panic
	handler.Delete(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}

	// User should be deleted
	if _, err := userRepo.GetByID("userA"); err == nil {
		t.Fatal("user should be deleted")
	}
}

func TestUserHandler_Delete_SyncsRuntimeNodeOwner(t *testing.T) {
	ctx := context.Background()
	userRepo := newMockUserRepo()
	atRepo := newMockAccessTokenRepo()
	nodeRepo := newTestNodeRepo()

	// Step 1: Create a ShardedNodeManager (real, from node package)
	nodeMgr := node.NewShardedNodeManager(4)

	// Step 2: Add an online node with OwnerUserID="userA" to nodeMgr
	onlineNode := &core.Node{
		ID: "RuntimeNode1", Name: "r1", Status: core.NodeStatusOnline, OwnerUserID: "userA",
		Tunnels: []core.Tunnel{},
	}
	if err := nodeMgr.Add(ctx, onlineNode); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Also add a second node owned by a different user (should NOT change)
	otherNode := &core.Node{
		ID: "RuntimeNodeOther", Name: "other", Status: core.NodeStatusOnline, OwnerUserID: "userB",
		Tunnels: []core.Tunnel{},
	}
	if err := nodeMgr.Add(ctx, otherNode); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Step 3: Create a userHandler with the nodeMgr
	user := &core.User{
		ID:        "userA",
		Username:  "alice",
		Status:    core.UserStatusActive,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	userRepo.Create(user, "fake-hash")

	handler := NewUserHandler(userRepo, nil, 10, nodeRepo, atRepo, nodeMgr)

	// Step 4: Delete userA
	req := reqWithClaims(http.MethodDelete, "/api/v1/users/userA", nil,
		&core.Claims{UserID: "admin", Roles: []string{"admin"}})
	w := httptest.NewRecorder()

	handler.Delete(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}

	// Step 5: Verify the node in nodeMgr now has OwnerUserID="system"
	gotNode, ok := nodeMgr.Get(ctx, "RuntimeNode1")
	if !ok {
		t.Fatal("RuntimeNode1 should still exist in nodeMgr")
	}
	if gotNode.OwnerUserID != "system" {
		t.Fatalf("expected RuntimeNode1 OwnerUserID=system, got %s", gotNode.OwnerUserID)
	}

	// Verify other user's node is unchanged
	gotOther, ok := nodeMgr.Get(ctx, "RuntimeNodeOther")
	if !ok {
		t.Fatal("RuntimeNodeOther should still exist in nodeMgr")
	}
	if gotOther.OwnerUserID != "userB" {
		t.Fatalf("RuntimeNodeOther should still be owned by userB, got %s", gotOther.OwnerUserID)
	}
}
