package auth

import (
	"context"
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
