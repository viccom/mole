package auth

import (
	"os"
	"testing"

	"github.com/nalgeon/redka"
	_ "modernc.org/sqlite"

	"moleAgent_Serv/internal/core"
)

func setupTestDB(t *testing.T) *redka.DB {
	t.Helper()
	path := t.TempDir() + "/test.db"
	opts := redka.Options{DriverName: "sqlite"}
	db, err := redka.Open(path, &opts)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	t.Cleanup(func() { db.Close(); os.Remove(path) })
	return db
}

func seedTestRoles(t *testing.T, db *redka.DB) core.RoleRepo {
	t.Helper()
	roleRepo := &testRoleRepo{db: db}

	adminRole := core.Role{
		ID:          "admin",
		Name:        "admin",
		Permissions: []core.Permission{{Resource: "*", Action: "*"}},
	}
	roleRepo.Create(&adminRole)

	viewerRole := core.Role{
		ID:          "viewer",
		Name:        "viewer",
		Permissions: []core.Permission{{Resource: "users", Action: "read"}},
	}
	roleRepo.Create(&viewerRole)

	return roleRepo
}

// 简化版 roleRepo 用于测试
type testRoleRepo struct {
	db *redka.DB
}

func (r *testRoleRepo) Create(role *core.Role) error {
	r.db.Hash().Set("roles", role.ID, `{"id":"`+role.ID+`","name":"`+role.Name+`"}`)
	return nil
}

func (r *testRoleRepo) GetByID(id string) (*core.Role, error) {
	val, err := r.db.Hash().Get("roles", id)
	if err != nil {
		return nil, core.ErrRoleNotFound
	}
	_ = val
	// 简化返回
	if id == "admin" {
		return &core.Role{ID: "admin", Name: "admin", Permissions: []core.Permission{{Resource: "*", Action: "*"}}}, nil
	}
	if id == "viewer" {
		return &core.Role{ID: "viewer", Name: "viewer", Permissions: []core.Permission{{Resource: "users", Action: "read"}}}, nil
	}
	return nil, core.ErrRoleNotFound
}

func (r *testRoleRepo) GetAll() ([]*core.Role, error) {
	return nil, nil
}

func (r *testRoleRepo) Update(role *core.Role) error {
	return nil
}

func (r *testRoleRepo) Delete(id string) error {
	r.db.Hash().Delete("roles", id)
	return nil
}

func TestRBACCheckPermission(t *testing.T) {
	db := setupTestDB(t)
	roleRepo := seedTestRoles(t, db)
	rbac := NewRBACEngine(db, roleRepo)

	// admin 用户通过 admin 角色（*:*）获得全部权限
	if err := rbac.AssignRole("admin", "admin"); err != nil {
		t.Fatalf("AssignRole failed: %v", err)
	}
	allowed, err := rbac.CheckPermission("admin", "users", "delete")
	if err != nil {
		t.Fatalf("CheckPermission failed: %v", err)
	}
	if !allowed {
		t.Error("admin should be allowed everything via admin role")
	}
}

func TestRBACAssignAndCheck(t *testing.T) {
	db := setupTestDB(t)
	roleRepo := seedTestRoles(t, db)
	rbac := NewRBACEngine(db, roleRepo)

	// 分配 viewer 角色给 user1
	err := rbac.AssignRole("user1", "viewer")
	if err != nil {
		t.Fatalf("AssignRole failed: %v", err)
	}

	// user1 应该能 read users
	allowed, err := rbac.CheckPermission("user1", "users", "read")
	if err != nil {
		t.Fatalf("CheckPermission failed: %v", err)
	}
	if !allowed {
		t.Error("user1 with viewer role should be able to read users")
	}

	// user1 不应该能 delete users
	allowed, err = rbac.CheckPermission("user1", "users", "delete")
	if err != nil {
		t.Fatalf("CheckPermission failed: %v", err)
	}
	if allowed {
		t.Error("user1 with viewer role should NOT be able to delete users")
	}
}

func TestRBACRevokeRole(t *testing.T) {
	db := setupTestDB(t)
	roleRepo := seedTestRoles(t, db)
	rbac := NewRBACEngine(db, roleRepo)

	rbac.AssignRole("user1", "viewer")
	rbac.RevokeRole("user1", "viewer")

	allowed, _ := rbac.CheckPermission("user1", "users", "read")
	if allowed {
		t.Error("user1 should not be able to read after role revocation")
	}
}

func TestRBACNoRoles(t *testing.T) {
	db := setupTestDB(t)
	roleRepo := seedTestRoles(t, db)
	rbac := NewRBACEngine(db, roleRepo)

	allowed, _ := rbac.CheckPermission("nobody", "users", "read")
	if allowed {
		t.Error("user with no roles should be denied")
	}
}
