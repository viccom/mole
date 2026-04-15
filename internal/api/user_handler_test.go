package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"moleAgent_Serv/internal/core"
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

	handler := NewUserHandler(userRepo, nil, 10, nodeRepo, atRepo)

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

	handler := NewUserHandler(userRepo, nil, 10, nodeRepo, atRepo)

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
	handler := NewUserHandler(userRepo, nil, 10, nil, nil)

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
