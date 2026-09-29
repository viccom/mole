package storage

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/nalgeon/redka"
	_ "modernc.org/sqlite"

	"moleAgent_Serv/internal/auth"
	"moleAgent_Serv/internal/config"
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
	repo := NewUserRepo(db)

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

// REL-07：级联删除（passwords/user_roles/usernames）单步失败必须上抛
// ——旧实现忽略这些错误，静默留下指向已删用户的孤儿数据
func TestUserRepoDelete_PropagatesCascadeErrors(t *testing.T) {
	setupTestDB(t)
	repo := NewUserRepo(db).(*userRepo)

	if err := repo.Create(&core.User{ID: "u1", Username: "alice", Status: "active"}, "hash"); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// 注入 user_roles 删除失败
	inner := repo.deleteField
	repo.deleteField = func(key, field string) error {
		if key == "user_roles" {
			return errors.New("simulated storage failure: disk full")
		}
		return inner(key, field)
	}

	err := repo.Delete("u1")
	if err == nil {
		t.Fatal("expected error when user_roles deletion fails")
	}
	if !strings.Contains(err.Error(), "simulated storage failure") {
		t.Fatalf("error should wrap the underlying failure, got: %v", err)
	}

	// users/passwords 在失败步骤之前，应已完成删除
	if _, err := repo.GetByID("u1"); err == nil {
		t.Fatal("user record should already be deleted")
	}
}

// Delete 前置 GetByID 的真错误必须上抛：吞错会把 existing 折叠成 nil，
// 跳过 usernames 索引清理（孤儿索引指向已删用户）且对调用方谎报成功。
// 注入手法：把 users 记录改写为非法 JSON——GetByID 解码失败是真 DB 语义
// 错误（≠ ErrUserNotFound），而 deleteField seam 保持可用
func TestUserRepoDelete_PropagatesGetByIDError(t *testing.T) {
	setupTestDB(t)
	repo := NewUserRepo(db).(*userRepo)

	if err := repo.Create(&core.User{ID: "u1", Username: "alice", Status: "active"}, "hash"); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if _, err := db.Hash().Set("users", "u1", "{not-valid-json"); err != nil {
		t.Fatalf("corrupt user record: %v", err)
	}

	if err := repo.Delete("u1"); err == nil {
		t.Fatal("Delete must propagate GetByID failure instead of swallowing it")
	}
}

// 「不存在」语义保持：删除不存在的用户是幂等 no-op（ErrUserNotFound 不上抛）
func TestUserRepoDelete_NonexistentIsNoError(t *testing.T) {
	setupTestDB(t)
	repo := NewUserRepo(db)
	if err := repo.Delete("ghost-user"); err != nil {
		t.Fatalf("Delete of nonexistent user must be a no-op, got %v", err)
	}
}

func TestUserRepoNotFound(t *testing.T) {
	setupTestDB(t)
	repo := NewUserRepo(db)

	_, err := repo.GetByID("nonexistent")
	if err == nil {
		t.Error("should fail for nonexistent user")
	}

	_, err = repo.GetByUsername("nonexistent")
	if err == nil {
		t.Error("should fail for nonexistent username")
	}
}

func TestUserRepoUpdate_RemovesOldUsernameIndex(t *testing.T) {
	setupTestDB(t)
	repo := NewUserRepo(db)

	user := &core.User{ID: "user-1", Username: "oldname", Status: core.UserStatusActive}
	if err := repo.Create(user, "hashedpw"); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	user.Username = "newname"
	if err := repo.Update(user); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	if _, err := repo.GetByUsername("oldname"); err == nil {
		t.Fatal("old username should no longer resolve after rename")
	}

	got, err := repo.GetByUsername("newname")
	if err != nil {
		t.Fatalf("new username should resolve: %v", err)
	}
	if got.ID != "user-1" {
		t.Fatalf("expected user-1, got %s", got.ID)
	}

	raw, err := db.Hash().Get("usernames", "oldname")
	if err == nil && raw.String() != "" {
		t.Fatalf("old username index should be deleted, got %q", raw.String())
	}
}

func TestRoleRepoCRUD(t *testing.T) {
	setupTestDB(t)
	repo := NewRoleRepo(db)

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
	repo := NewNodeRepo(db)

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
	repo := NewRoleRepo(db)
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
	userRepo := NewUserRepo(db)
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

// ===== SEC-05：默认口令强制轮换 =====

// seedLegacyDefaultAdmin 构造历史遗留状态：admin 用户存在且哈希仍是
// 公开默认口令 "admin"
func seedLegacyDefaultAdmin(t *testing.T, password string) {
	t.Helper()
	user := core.User{ID: "admin", Username: "admin", Status: core.UserStatusActive}
	data, err := json.Marshal(user)
	if err != nil {
		t.Fatalf("marshal admin user: %v", err)
	}
	if _, err := db.Hash().Set("users", "admin", string(data)); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	hash, err := auth.HashPassword(password, 4)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if _, err := db.Hash().Set("passwords", "admin", hash); err != nil {
		t.Fatalf("seed passwords: %v", err)
	}
}

func readAdminHash(t *testing.T) string {
	t.Helper()
	val, err := db.Hash().Get("passwords", "admin")
	if err != nil {
		t.Fatalf("read admin hash: %v", err)
	}
	return val.String()
}

// 库内哈希仍是默认口令且 MA_ADMIN_PASS 未设 → 轮换为随机口令
func TestSeedData_RotatesLegacyDefaultAdminPassword(t *testing.T) {
	setupTestDB(t)
	t.Setenv("MA_ADMIN_USER", "")
	t.Setenv("MA_ADMIN_PASS", "")
	seedLegacyDefaultAdmin(t, "admin")

	if err := seedData(); err != nil {
		t.Fatalf("seedData failed: %v", err)
	}

	hash := readAdminHash(t)
	if auth.VerifyPassword("admin", hash) {
		t.Fatal("legacy default password must be rotated away")
	}
	// 轮换后的哈希必须与本次启动生成的随机口令匹配（同一 once 值）
	_, generated := config.AdminUser()
	if !auth.VerifyPassword(generated, hash) {
		t.Fatal("rotated hash must match the generated random password")
	}
}

// 库内哈希仍是默认口令且 MA_ADMIN_PASS 已设 → 用环境值轮换（无缝换密通道）
func TestSeedData_RotatesDefaultAdminPasswordFromEnv(t *testing.T) {
	setupTestDB(t)
	t.Setenv("MA_ADMIN_USER", "")
	t.Setenv("MA_ADMIN_PASS", "env-chosen-pass")
	seedLegacyDefaultAdmin(t, "admin")

	if err := seedData(); err != nil {
		t.Fatalf("seedData failed: %v", err)
	}

	hash := readAdminHash(t)
	if auth.VerifyPassword("admin", hash) {
		t.Fatal("default password must be rotated away")
	}
	if !auth.VerifyPassword("env-chosen-pass", hash) {
		t.Fatal("rotated hash must match MA_ADMIN_PASS")
	}
}

// 已改密用户（哈希非默认口令）→ 不动，即使 MA_ADMIN_PASS 已设
func TestSeedData_KeepsCustomizedAdminPassword(t *testing.T) {
	setupTestDB(t)
	t.Setenv("MA_ADMIN_USER", "")
	t.Setenv("MA_ADMIN_PASS", "env-pass-attempt")
	seedLegacyDefaultAdmin(t, "custom-pass-by-user")

	if err := seedData(); err != nil {
		t.Fatalf("seedData failed: %v", err)
	}

	hash := readAdminHash(t)
	if !auth.VerifyPassword("custom-pass-by-user", hash) {
		t.Fatal("customized password must be left untouched")
	}
	if auth.VerifyPassword("env-pass-attempt", hash) {
		t.Fatal("MA_ADMIN_PASS must not override a customized password")
	}
}
