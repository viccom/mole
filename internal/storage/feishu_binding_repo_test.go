package storage

import (
	"testing"
	"time"

	"moleAgent_Serv/internal/core"
)

func TestFeishuBindingRepoCRUD(t *testing.T) {
	setupTestDB(t)
	repo := NewFeishuBindingRepo(db)

	binding := &core.FeishuBinding{
		OpenID:     "ou_test123",
		UserID:     "user1",
		FeishuName: "Test User",
		AvatarURL:  "https://example.com/avatar.png",
		BoundAt:    time.Now().UTC(),
	}

	if err := repo.Create(binding); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := repo.GetByOpenID("ou_test123")
	if err != nil {
		t.Fatalf("GetByOpenID failed: %v", err)
	}
	if got.UserID != "user1" {
		t.Errorf("expected user1, got %s", got.UserID)
	}
	if got.FeishuName != "Test User" {
		t.Errorf("expected Test User, got %s", got.FeishuName)
	}

	got2, err := repo.GetByUserID("user1")
	if err != nil {
		t.Fatalf("GetByUserID failed: %v", err)
	}
	if got2.OpenID != "ou_test123" {
		t.Errorf("expected ou_test123, got %s", got2.OpenID)
	}
}

func TestFeishuBindingRepoNotFound(t *testing.T) {
	setupTestDB(t)
	repo := NewFeishuBindingRepo(db)

	_, err := repo.GetByOpenID("nonexistent")
	if err == nil {
		t.Error("should fail for nonexistent open_id")
	}

	_, err = repo.GetByUserID("nonexistent")
	if err == nil {
		t.Error("should fail for nonexistent user_id")
	}
}

func TestFeishuBindingRepoDeleteByOpenID(t *testing.T) {
	setupTestDB(t)
	repo := NewFeishuBindingRepo(db)

	binding := &core.FeishuBinding{
		OpenID:     "ou_del_test",
		UserID:     "user_del",
		FeishuName: "Delete Me",
		BoundAt:    time.Now().UTC(),
	}
	repo.Create(binding)

	if err := repo.DeleteByOpenID("ou_del_test"); err != nil {
		t.Fatalf("DeleteByOpenID failed: %v", err)
	}

	_, err := repo.GetByOpenID("ou_del_test")
	if err == nil {
		t.Error("should fail after delete")
	}
	_, err = repo.GetByUserID("user_del")
	if err == nil {
		t.Error("user index should also be cleaned")
	}
}

func TestFeishuBindingRepoDeleteByUserID(t *testing.T) {
	setupTestDB(t)
	repo := NewFeishuBindingRepo(db)

	binding := &core.FeishuBinding{
		OpenID:     "ou_user_del",
		UserID:     "user_byid",
		FeishuName: "Delete By User",
		BoundAt:    time.Now().UTC(),
	}
	repo.Create(binding)

	if err := repo.DeleteByUserID("user_byid"); err != nil {
		t.Fatalf("DeleteByUserID failed: %v", err)
	}

	_, err := repo.GetByOpenID("ou_user_del")
	if err == nil {
		t.Error("should fail after delete")
	}
}
