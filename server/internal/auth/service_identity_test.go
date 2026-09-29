package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nalgeon/redka"
	_ "modernc.org/sqlite"

	"moleAgent_Serv/internal/core"
)

type memoryUserRepo struct {
	users    map[string]*core.User
	byName   map[string]string
	password map[string]string
}

func newMemoryUserRepo() *memoryUserRepo {
	return &memoryUserRepo{
		users:    make(map[string]*core.User),
		byName:   make(map[string]string),
		password: make(map[string]string),
	}
}

func (r *memoryUserRepo) Create(user *core.User, passwordHash string) error {
	cloned := *user
	r.users[user.ID] = &cloned
	r.byName[user.Username] = user.ID
	r.password[user.ID] = passwordHash
	return nil
}

func (r *memoryUserRepo) GetByID(id string) (*core.User, error) {
	u, ok := r.users[id]
	if !ok {
		return nil, core.ErrUserNotFound
	}
	cloned := *u
	return &cloned, nil
}

func (r *memoryUserRepo) GetByUsername(username string) (*core.User, error) {
	id, ok := r.byName[username]
	if !ok {
		return nil, core.ErrUserNotFound
	}
	return r.GetByID(id)
}

func (r *memoryUserRepo) GetAll() ([]*core.User, error) {
	var out []*core.User
	for _, u := range r.users {
		cloned := *u
		out = append(out, &cloned)
	}
	return out, nil
}

func (r *memoryUserRepo) Update(user *core.User) error {
	cloned := *user
	r.users[user.ID] = &cloned
	r.byName[user.Username] = user.ID
	return nil
}

func (r *memoryUserRepo) Delete(id string) error {
	if u, ok := r.users[id]; ok {
		delete(r.byName, u.Username)
	}
	delete(r.users, id)
	delete(r.password, id)
	return nil
}

func (r *memoryUserRepo) GetPasswordHash(userID string) (string, error) {
	hash, ok := r.password[userID]
	if !ok {
		return "", core.ErrUserNotFound
	}
	return hash, nil
}

func (r *memoryUserRepo) SetPasswordHash(userID, passwordHash string) error {
	r.password[userID] = passwordHash
	return nil
}

// SEC-04：refresh 必须回库重查角色——旧 claims 的 Roles 是签发时快照，
// 撤销角色后照抄续期会让权限收回形同虚设
func TestAuthServiceRefreshToken_ReloadsRolesFromStore(t *testing.T) {
	db, err := redka.Open(":memory:", &redka.Options{DriverName: "sqlite"})
	if err != nil {
		t.Fatalf("failed to open in-memory redka: %v", err)
	}
	defer db.Close()

	userRepo := newMemoryUserRepo()
	roleRepo := &mockRoleRepo{roles: map[string]*core.Role{
		"role-a": {ID: "role-a", Name: "role-a"},
		"role-b": {ID: "role-b", Name: "role-b"},
	}}
	rbac := NewRBACEngine(db, roleRepo)
	jwtMgr := NewJWTManager("test-secret", time.Hour)
	authSvc := NewAuthService(jwtMgr, userRepo, roleRepo, rbac, db)

	hash, err := HashPassword("secret-pass", 4)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}
	userID := "uuid-user-002"
	if err := userRepo.Create(&core.User{
		ID:       userID,
		Username: "role-holder",
		Status:   core.UserStatusActive,
	}, hash); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := rbac.AssignRole(userID, "role-a"); err != nil {
		t.Fatalf("AssignRole failed: %v", err)
	}

	token, _, err := authSvc.Login(context.Background(), "role-holder", "secret-pass")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	claims, err := authSvc.VerifyToken(token)
	if err != nil {
		t.Fatalf("VerifyToken failed: %v", err)
	}
	if len(claims.Roles) != 1 || claims.Roles[0] != "role-a" {
		t.Fatalf("expected initial roles [role-a], got %v", claims.Roles)
	}

	// 库中角色从 A 改为 B 后刷新 token
	if err := rbac.RevokeRole(userID, "role-a"); err != nil {
		t.Fatalf("RevokeRole failed: %v", err)
	}
	if err := rbac.AssignRole(userID, "role-b"); err != nil {
		t.Fatalf("AssignRole failed: %v", err)
	}

	newToken, err := authSvc.RefreshToken(token)
	if err != nil {
		t.Fatalf("RefreshToken failed: %v", err)
	}
	newClaims, err := authSvc.VerifyToken(newToken)
	if err != nil {
		t.Fatalf("VerifyToken(new) failed: %v", err)
	}
	if len(newClaims.Roles) != 1 || newClaims.Roles[0] != "role-b" {
		t.Fatalf("expected refreshed roles [role-b], got %v", newClaims.Roles)
	}
}

// SEC-14：自助改密对新密码执行与 Create/Update/resetPassword 相同的强度校验
func TestAuthServiceChangePassword_EnforcesStrength(t *testing.T) {
	db, err := redka.Open(":memory:", &redka.Options{DriverName: "sqlite"})
	if err != nil {
		t.Fatalf("failed to open in-memory redka: %v", err)
	}
	defer db.Close()

	userRepo := newMemoryUserRepo()
	roleRepo := &mockRoleRepo{roles: map[string]*core.Role{}}
	rbac := NewRBACEngine(db, roleRepo)
	authSvc := NewAuthService(NewJWTManager("test-secret", time.Hour), userRepo, roleRepo, rbac, db)

	hash, err := HashPassword("old-pass-123", 4)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}
	if err := userRepo.Create(&core.User{ID: "u1", Username: "alice", Status: core.UserStatusActive}, hash); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// 3 位新密码 → 拒绝
	if err := authSvc.ChangePassword(context.Background(), "u1", "old-pass-123", "abc"); err == nil {
		t.Fatal("expected error for 3-char new password")
	}
	// 旧密码应仍然有效
	gotHash, _ := userRepo.GetPasswordHash("u1")
	if !VerifyPassword("old-pass-123", gotHash) {
		t.Fatal("old password should remain effective after rejected change")
	}

	// 8+ 位 → 通过
	if err := authSvc.ChangePassword(context.Background(), "u1", "old-pass-123", "good-pass-123"); err != nil {
		t.Fatalf("expected success for 8+ char password, got %v", err)
	}
	gotHash, _ = userRepo.GetPasswordHash("u1")
	if !VerifyPassword("good-pass-123", gotHash) {
		t.Fatal("new password should be effective after change")
	}
}

// QUA-06：用户不存在与密码错误必须返回同一错误文案（防用户名枚举）
func TestAuthServiceLogin_UserNotFoundAndWrongPasswordSameError(t *testing.T) {
	db, err := redka.Open(":memory:", &redka.Options{DriverName: "sqlite"})
	if err != nil {
		t.Fatalf("failed to open in-memory redka: %v", err)
	}
	defer db.Close()

	userRepo := newMemoryUserRepo()
	roleRepo := &mockRoleRepo{roles: map[string]*core.Role{}}
	authSvc := NewAuthService(NewJWTManager("test-secret", time.Hour), userRepo, roleRepo, NewRBACEngine(db, roleRepo), db)

	hash, err := HashPassword("secret-pass", 4)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}
	if err := userRepo.Create(&core.User{ID: "u1", Username: "alice", Status: core.UserStatusActive}, hash); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, _, errNotFound := authSvc.Login(context.Background(), "no-such-user", "whatever-pass")
	_, _, errWrongPass := authSvc.Login(context.Background(), "alice", "wrong-pass")
	if errNotFound == nil || errWrongPass == nil {
		t.Fatalf("both paths must fail: notFound=%v wrongPass=%v", errNotFound, errWrongPass)
	}
	if !errors.Is(errNotFound, core.ErrInvalidCredentials) || !errors.Is(errWrongPass, core.ErrInvalidCredentials) {
		t.Fatalf("both paths must return ErrInvalidCredentials: %v / %v", errNotFound, errWrongPass)
	}
	if errNotFound.Error() != errWrongPass.Error() {
		t.Fatalf("error text must be identical to prevent enumeration: %q vs %q", errNotFound.Error(), errWrongPass.Error())
	}
}

// QUA-06：用户不存在路径必须执行一次等价 bcrypt 比较——无比较时该路径
// 微秒级返回，cost 12 的比较至少数十毫秒（下界 10ms 断言，防快路径泄露）
func TestAuthServiceLogin_UserNotFoundPerformsBcryptWork(t *testing.T) {
	db, err := redka.Open(":memory:", &redka.Options{DriverName: "sqlite"})
	if err != nil {
		t.Fatalf("failed to open in-memory redka: %v", err)
	}
	defer db.Close()

	userRepo := newMemoryUserRepo()
	roleRepo := &mockRoleRepo{roles: map[string]*core.Role{}}
	authSvc := NewAuthService(NewJWTManager("test-secret", time.Hour), userRepo, roleRepo, NewRBACEngine(db, roleRepo), db)

	start := time.Now()
	_, _, err = authSvc.Login(context.Background(), "no-such-user", "whatever-pass")
	elapsed := time.Since(start)
	if !errors.Is(err, core.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
	if elapsed < 10*time.Millisecond {
		t.Fatalf("user-not-found path should perform bcrypt work, returned in %v", elapsed)
	}
}

// QUA-06 残余：禁用用户与哈希缺失两条快路径补 dummy bcrypt 后，错误文案
// 必须保持不变（行为守卫）——修复只加时序功，不改对外语义：
//   - 禁用路径仍回 ErrUserDisabled（auth handler 与 ErrInvalidCredentials
//     同样映射为 401 "Invalid username or password"，HTTP 层文案一致）
//   - 哈希缺失路径仍回 ErrInvalidCredentials，与「密码错误」逐字一致
func TestAuthServiceLogin_FastPathsKeepCredentialErrorText(t *testing.T) {
	db, err := redka.Open(":memory:", &redka.Options{DriverName: "sqlite"})
	if err != nil {
		t.Fatalf("failed to open in-memory redka: %v", err)
	}
	defer db.Close()

	userRepo := newMemoryUserRepo()
	roleRepo := &mockRoleRepo{roles: map[string]*core.Role{}}
	authSvc := NewAuthService(NewJWTManager("test-secret", time.Hour), userRepo, roleRepo, NewRBACEngine(db, roleRepo), db)

	// 禁用用户（哈希存在）
	if err := userRepo.Create(&core.User{ID: "u-dis", Username: "disabled-user", Status: core.UserStatusDisabled}, "some-hash"); err != nil {
		t.Fatalf("Create disabled user: %v", err)
	}
	// 活跃用户但哈希缺失
	if err := userRepo.Create(&core.User{ID: "u-nohash", Username: "nohash-user", Status: core.UserStatusActive}, ""); err != nil {
		t.Fatalf("Create nohash user: %v", err)
	}
	delete(userRepo.password, "u-nohash")
	// 活跃用户 + 错误密码（对照路径）
	hash, err := HashPassword("right-pass-123", 4)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := userRepo.Create(&core.User{ID: "u-wp", Username: "wrongpass-user", Status: core.UserStatusActive}, hash); err != nil {
		t.Fatalf("Create wrongpass user: %v", err)
	}

	_, _, errDisabled := authSvc.Login(context.Background(), "disabled-user", "whatever-pass")
	if !errors.Is(errDisabled, core.ErrUserDisabled) {
		t.Fatalf("disabled path must keep ErrUserDisabled semantics, got %v", errDisabled)
	}

	_, _, errNoHash := authSvc.Login(context.Background(), "nohash-user", "whatever-pass")
	_, _, errWrongPass := authSvc.Login(context.Background(), "wrongpass-user", "bad-pass")
	if !errors.Is(errNoHash, core.ErrInvalidCredentials) || !errors.Is(errWrongPass, core.ErrInvalidCredentials) {
		t.Fatalf("no-hash and wrong-password must both be ErrInvalidCredentials: %v / %v", errNoHash, errWrongPass)
	}
	if errNoHash.Error() != errWrongPass.Error() {
		t.Fatalf("no-hash path text must equal wrong-password text (anti-enumeration), %q vs %q", errNoHash.Error(), errWrongPass.Error())
	}
}

func TestAuthServiceLogin_PreservesUserIDAndUsernameSeparation(t *testing.T) {
	db, err := redka.Open(":memory:", &redka.Options{DriverName: "sqlite"})
	if err != nil {
		t.Fatalf("failed to open in-memory redka: %v", err)
	}
	defer db.Close()

	userRepo := newMemoryUserRepo()
	roleRepo := &mockRoleRepo{roles: map[string]*core.Role{}}
	rbac := NewRBACEngine(db, roleRepo)
	jwtMgr := NewJWTManager("test-secret", time.Hour)
	authSvc := NewAuthService(jwtMgr, userRepo, roleRepo, rbac, db)

	hash, err := HashPassword("secret-pass", 4)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}
	user := &core.User{
		ID:       "uuid-user-001",
		Username: "operator-display-name",
		Status:   core.UserStatusActive,
	}
	if err := userRepo.Create(user, hash); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	token, _, err := authSvc.Login(context.Background(), "operator-display-name", "secret-pass")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	claims, err := authSvc.VerifyToken(token)
	if err != nil {
		t.Fatalf("VerifyToken failed: %v", err)
	}
	if claims.UserID != "uuid-user-001" {
		t.Fatalf("expected userID uuid-user-001, got %s", claims.UserID)
	}
	if claims.Username != "operator-display-name" {
		t.Fatalf("expected username operator-display-name, got %s", claims.Username)
	}
	if claims.UserID == claims.Username {
		t.Fatal("expected userID and username to remain distinct")
	}
}
