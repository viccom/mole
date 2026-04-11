package storage

import (
	"os"
	"testing"

	"github.com/nalgeon/redka"
	_ "modernc.org/sqlite"

	"moleAgent_Serv/internal/auth"
	"moleAgent_Serv/internal/core"
)

func setupTestDB(t *testing.T) {
	t.Helper()
	path := t.TempDir() + "/test.db"
	opts := redka.Options{DriverName: "sqlite"}
	var err error
	db, err = redka.Open(path, &opts)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		os.Remove(path)
	})
}

func TestUserRepoCRUD(t *testing.T) {
	setupTestDB(t)
	repo := NewUserRepo()

	// Create
	user := &core.User{ID: "test1", Username: "testuser", Status: "active"}
	if err := repo.Create(user, "hashedpw"); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// GetByID
	got, err := repo.GetByID("test1")
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if got.Username != "testuser" {
		t.Errorf("expected testuser, got %s", got.Username)
	}

	// GetByUsername
	got, err = repo.GetByUsername("testuser")
	if err != nil {
		t.Fatalf("GetByUsername failed: %v", err)
	}
	if got.ID != "test1" {
		t.Errorf("expected test1, got %s", got.ID)
	}

	// GetAll
	all, err := repo.GetAll()
	if err != nil {
		t.Fatalf("GetAll failed: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("expected 1 user, got %d", len(all))
	}

	// Update
	user.Username = "updated"
	if err := repo.Update(user); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	got, _ = repo.GetByID("test1")
	if got.Username != "updated" {
		t.Errorf("expected updated, got %s", got.Username)
	}

	// Password
	if err := repo.SetPasswordHash("test1", "newhash"); err != nil {
		t.Fatalf("SetPasswordHash failed: %v", err)
	}
	hash, err := repo.GetPasswordHash("test1")
	if err != nil {
		t.Fatalf("GetPasswordHash failed: %v", err)
	}
	if hash != "newhash" {
		t.Errorf("expected newhash, got %s", hash)
	}

	// Delete
	if err := repo.Delete("test1"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	_, err = repo.GetByID("test1")
	if err == nil {
		t.Error("should fail after delete")
	}
}

func TestUserRepoNotFound(t *testing.T) {
	setupTestDB(t)
	repo := NewUserRepo()

	_, err := repo.GetByID("nonexistent")
	if err == nil {
		t.Error("should fail for nonexistent user")
	}

	_, err = repo.GetByUsername("nonexistent")
	if err == nil {
		t.Error("should fail for nonexistent username")
	}
}

func TestRoleRepoCRUD(t *testing.T) {
	setupTestDB(t)
	repo := NewRoleRepo()

	role := &core.Role{
		ID:          "test-role",
		Name:        "test",
		Description: "Test role",
		Permissions: []core.Permission{{Resource: "users", Action: "read"}},
	}

	if err := repo.Create(role); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := repo.GetByID("test-role")
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if got.Name != "test" {
		t.Errorf("expected test, got %s", got.Name)
	}

	all, err := repo.GetAll()
	if err != nil {
		t.Fatalf("GetAll failed: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("expected 1 role, got %d", len(all))
	}

	role.Description = "Updated"
	if err := repo.Update(role); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	got, _ = repo.GetByID("test-role")
	if got.Description != "Updated" {
		t.Errorf("expected Updated, got %s", got.Description)
	}

	if err := repo.Delete("test-role"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	_, err = repo.GetByID("test-role")
	if err == nil {
		t.Error("should fail after delete")
	}
}

func TestNodeRepoCRUD(t *testing.T) {
	setupTestDB(t)
	repo := NewNodeRepo()

	n := &core.Node{
		ID:     "node1",
		Name:   "Test Node",
		Status: core.NodeStatusOffline,
	}

	if err := repo.Create(n); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := repo.GetByID("node1")
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if got.Name != "Test Node" {
		t.Errorf("expected Test Node, got %s", got.Name)
	}

	all, err := repo.GetAll()
	if err != nil {
		t.Fatalf("GetAll failed: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("expected 1 node, got %d", len(all))
	}

	n.Status = core.NodeStatusOnline
	if err := repo.Update(n); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	got, _ = repo.GetByID("node1")
	if got.Status != core.NodeStatusOnline {
		t.Errorf("expected online, got %s", got.Status)
	}

	if err := repo.Delete("node1"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	_, err = repo.GetByID("node1")
	if err == nil {
		t.Error("should fail after delete")
	}
}

func TestPasswordHashAndVerify(t *testing.T) {
	hash, err := auth.HashPassword("testpass", 10)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}
	if hash == "" {
		t.Error("hash should not be empty")
	}
	if !auth.VerifyPassword("testpass", hash) {
		t.Error("VerifyPassword should return true for correct password")
	}
	if auth.VerifyPassword("wrong", hash) {
		t.Error("VerifyPassword should return false for wrong password")
	}
}

func TestSeedData(t *testing.T) {
	setupTestDB(t)
	if err := seedData(); err != nil {
		t.Fatalf("seedData failed: %v", err)
	}

	// 验证 admin 角色存在
	repo := NewRoleRepo()
	role, err := repo.GetByID("admin")
	if err != nil {
		t.Fatalf("admin role not found: %v", err)
	}
	if role.Name != "admin" {
		t.Errorf("expected admin, got %s", role.Name)
	}

	// 验证 operator 角色
	role, err = repo.GetByID("operator")
	if err != nil {
		t.Fatalf("operator role not found: %v", err)
	}
	if role.Name != "operator" {
		t.Errorf("expected operator, got %s", role.Name)
	}

	// 验证 admin 用户
	userRepo := NewUserRepo()
	user, err := userRepo.GetByID("admin")
	if err != nil {
		t.Fatalf("admin user not found: %v", err)
	}
	if user.Username != "admin" {
		t.Errorf("expected admin, got %s", user.Username)
	}

	// 验证密码已设置
	hash, err := userRepo.GetPasswordHash("admin")
	if err != nil {
		t.Fatalf("admin password not found: %v", err)
	}
	if hash == "" {
		t.Error("admin password hash should not be empty")
	}

	// 验证幂等性 — 再次 seed 不应报错
	if err := seedData(); err != nil {
		t.Fatalf("seedData should be idempotent: %v", err)
	}
}
