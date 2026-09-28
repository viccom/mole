package storage

import (
	"fmt"
	"testing"
	"time"

	"moleAgent_Serv/internal/core"
)

func newTestToken(userID, name string) *core.AccessToken {
	now := time.Now().UTC()
	return &core.AccessToken{
		ID:          fmt.Sprintf("atk_%d_%s", now.UnixNano(), name),
		UserID:      userID,
		Name:        name,
		TokenPrefix: "mat_abcd",
		TokenHash:   GenerateTokenHash("mat_testraw_" + name),
		Status:      core.AccessTokenActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func TestAccessTokenRepo_CreateAndGetByID(t *testing.T) {
	setupTestDB(t)
	repo := NewAccessTokenRepo(db)

	token := newTestToken("user-1", "mytoken")
	if err := repo.Create(token); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := repo.GetByID(token.ID)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}

	if got.ID != token.ID {
		t.Errorf("ID: expected %s, got %s", token.ID, got.ID)
	}
	if got.UserID != token.UserID {
		t.Errorf("UserID: expected %s, got %s", token.UserID, got.UserID)
	}
	if got.Name != token.Name {
		t.Errorf("Name: expected %s, got %s", token.Name, got.Name)
	}
	if got.TokenPrefix != token.TokenPrefix {
		t.Errorf("TokenPrefix: expected %s, got %s", token.TokenPrefix, got.TokenPrefix)
	}
	if got.TokenHash != token.TokenHash {
		t.Errorf("TokenHash: expected %s, got %s", token.TokenHash, got.TokenHash)
	}
	if got.Status != token.Status {
		t.Errorf("Status: expected %s, got %s", token.Status, got.Status)
	}
	if !got.CreatedAt.Equal(token.CreatedAt) {
		t.Errorf("CreatedAt: expected %v, got %v", token.CreatedAt, got.CreatedAt)
	}
	if !got.UpdatedAt.Equal(token.UpdatedAt) {
		t.Errorf("UpdatedAt: expected %v, got %v", token.UpdatedAt, got.UpdatedAt)
	}
}

func TestAccessTokenRepo_Create_SetsHashIndex(t *testing.T) {
	setupTestDB(t)
	repo := NewAccessTokenRepo(db)

	token := newTestToken("user-1", "hashtest")
	if err := repo.Create(token); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	val, err := db.Hash().Get("access_token_hash_index", token.TokenHash)
	if err != nil {
		t.Fatalf("failed to read hash index: %v", err)
	}
	if val.String() != token.ID {
		t.Errorf("hash index: expected %s, got %s", token.ID, val.String())
	}
}

func TestAccessTokenRepo_GetByID_NotFound(t *testing.T) {
	setupTestDB(t)
	repo := NewAccessTokenRepo(db)

	_, err := repo.GetByID("nonexistent")
	if err != core.ErrNotFound {
		t.Errorf("expected core.ErrNotFound, got %v", err)
	}
}

func TestAccessTokenRepo_GetByHash(t *testing.T) {
	setupTestDB(t)
	repo := NewAccessTokenRepo(db)

	raw := "mat_testraw_searchme"
	token := newTestToken("user-1", "searchme")
	token.TokenHash = GenerateTokenHash(raw)
	if err := repo.Create(token); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := repo.GetByHash(GenerateTokenHash(raw))
	if err != nil {
		t.Fatalf("GetByHash failed: %v", err)
	}
	if got.ID != token.ID {
		t.Errorf("expected ID %s, got %s", token.ID, got.ID)
	}
}

func TestAccessTokenRepo_GetByHash_NotFound(t *testing.T) {
	setupTestDB(t)
	repo := NewAccessTokenRepo(db)

	_, err := repo.GetByHash("nonexistent")
	if err != core.ErrNotFound {
		t.Errorf("expected core.ErrNotFound, got %v", err)
	}
}

func TestAccessTokenRepo_ListByUser(t *testing.T) {
	setupTestDB(t)
	repo := NewAccessTokenRepo(db)

	tokens := []*core.AccessToken{
		newTestToken("userA", "token-a1"),
		newTestToken("userA", "token-a2"),
		newTestToken("userB", "token-b1"),
	}
	for _, tk := range tokens {
		if err := repo.Create(tk); err != nil {
			t.Fatalf("Create failed: %v", err)
		}
	}

	listA, err := repo.ListByUser("userA")
	if err != nil {
		t.Fatalf("ListByUser(userA) failed: %v", err)
	}
	if len(listA) != 2 {
		t.Errorf("expected 2 tokens for userA, got %d", len(listA))
	}

	listB, err := repo.ListByUser("userB")
	if err != nil {
		t.Fatalf("ListByUser(userB) failed: %v", err)
	}
	if len(listB) != 1 {
		t.Errorf("expected 1 token for userB, got %d", len(listB))
	}
}

func TestAccessTokenRepo_ListByUser_Empty(t *testing.T) {
	setupTestDB(t)
	repo := NewAccessTokenRepo(db)

	list, err := repo.ListByUser("nobody")
	if err != nil {
		t.Fatalf("ListByUser failed: %v", err)
	}
	// ListByUser returns nil when no tokens match (no append to nil slice).
	// This is acceptable; callers should treat nil and empty slices equivalently.
	if len(list) != 0 {
		t.Errorf("expected empty slice, got %d items", len(list))
	}
}

// SEC-01：proof 认证需要全量候选列举（跨用户、含禁用记录由调用方过滤）
func TestAccessTokenRepo_ListAll(t *testing.T) {
	setupTestDB(t)
	repo := NewAccessTokenRepo(db)

	// 空库：返回空集
	all, err := repo.ListAll()
	if err != nil {
		t.Fatalf("ListAll on empty db failed: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("expected empty result, got %d items", len(all))
	}

	tokens := []*core.AccessToken{
		newTestToken("userA", "token-a1"),
		newTestToken("userB", "token-b1"),
	}
	for _, tk := range tokens {
		if err := repo.Create(tk); err != nil {
			t.Fatalf("Create failed: %v", err)
		}
	}

	all, err = repo.ListAll()
	if err != nil {
		t.Fatalf("ListAll failed: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 tokens across users, got %d", len(all))
	}
	gotIDs := map[string]bool{}
	for _, tk := range all {
		gotIDs[tk.ID] = true
	}
	for _, tk := range tokens {
		if !gotIDs[tk.ID] {
			t.Errorf("token %s missing from ListAll result", tk.ID)
		}
	}
}

func TestAccessTokenRepo_Update(t *testing.T) {
	setupTestDB(t)
	repo := NewAccessTokenRepo(db)

	token := newTestToken("user-1", "updateme")
	if err := repo.Create(token); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	originalUpdatedAt := token.UpdatedAt

	token.Status = core.AccessTokenDisabled
	token.Name = "updated-name"
	if err := repo.Update(token); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	got, err := repo.GetByID(token.ID)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if got.Status != core.AccessTokenDisabled {
		t.Errorf("Status: expected %s, got %s", core.AccessTokenDisabled, got.Status)
	}
	if got.Name != "updated-name" {
		t.Errorf("Name: expected updated-name, got %s", got.Name)
	}
	if !got.UpdatedAt.After(originalUpdatedAt) {
		t.Errorf("UpdatedAt should be after original; original=%v, got=%v", originalUpdatedAt, got.UpdatedAt)
	}
}

func TestAccessTokenRepo_Delete(t *testing.T) {
	setupTestDB(t)
	repo := NewAccessTokenRepo(db)

	token := newTestToken("user-1", "deleteme")
	if err := repo.Create(token); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if err := repo.Delete(token.ID); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err := repo.GetByID(token.ID)
	if err != core.ErrNotFound {
		t.Errorf("expected core.ErrNotFound after delete, got %v", err)
	}

	// 验证 hash 索引也被清理（删除后索引不应存在）
	_, err = db.Hash().Get("access_token_hash_index", token.TokenHash)
	if err == nil {
		t.Error("hash index entry should be removed after delete")
	}
}

func TestAccessTokenRepo_Delete_NotFound(t *testing.T) {
	setupTestDB(t)
	repo := NewAccessTokenRepo(db)

	err := repo.Delete("nonexistent")
	if err != nil {
		t.Errorf("Delete of nonexistent ID should not error, got %v", err)
	}
}

func TestAccessTokenRepo_Update_MaintainsHashIndex(t *testing.T) {
	setupTestDB(t)
	repo := NewAccessTokenRepo(db)

	// Step 1: Create a token with an initial hash
	originalRaw := "mat_testraw_rotation_test"
	token := newTestToken("user-1", "rotate-me")
	token.TokenHash = GenerateTokenHash(originalRaw)
	if err := repo.Create(token); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Step 2: Verify original hash finds it via GetByHash
	got, err := repo.GetByHash(GenerateTokenHash(originalRaw))
	if err != nil {
		t.Fatalf("GetByHash with original hash should succeed: %v", err)
	}
	if got.ID != token.ID {
		t.Errorf("expected ID %s, got %s", token.ID, got.ID)
	}

	// Step 3: Update the token with a new TokenHash (simulating rotation)
	newRaw := "mat_testraw_rotated_brand_new"
	token.TokenHash = GenerateTokenHash(newRaw)
	if err := repo.Update(token); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// Step 4: Verify the OLD hash no longer finds it (ErrNotFound)
	_, err = repo.GetByHash(GenerateTokenHash(originalRaw))
	if err != core.ErrNotFound {
		t.Errorf("expected ErrNotFound for old hash after update, got %v", err)
	}

	// Step 5: Verify the NEW hash finds it correctly
	got, err = repo.GetByHash(GenerateTokenHash(newRaw))
	if err != nil {
		t.Fatalf("GetByHash with new hash should succeed: %v", err)
	}
	if got.ID != token.ID {
		t.Errorf("expected ID %s, got %s", token.ID, got.ID)
	}

	// Step 6: Verify persisted record also contains the latest TokenHash
	gotByID, err := repo.GetByID(token.ID)
	if err != nil {
		t.Fatalf("GetByID after rotation should succeed: %v", err)
	}
	if gotByID.TokenHash != token.TokenHash {
		t.Fatalf("expected persisted TokenHash=%s, got %s", token.TokenHash, gotByID.TokenHash)
	}
}

func TestGenerateTokenHash(t *testing.T) {
	h1 := GenerateTokenHash("hello")
	h2 := GenerateTokenHash("hello")
	if h1 != h2 {
		t.Errorf("hash should be deterministic: %s != %s", h1, h2)
	}

	if len(h1) != 64 {
		t.Errorf("expected 64-char hex string (sha256), got %d chars", len(h1))
	}

	h3 := GenerateTokenHash("world")
	if h1 == h3 {
		t.Error("different inputs should produce different hashes")
	}
}
