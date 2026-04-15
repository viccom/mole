package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/storage"
)

// ---------------------------------------------------------------------------
// Mock repo
// ---------------------------------------------------------------------------

type mockAccessTokenRepo struct {
	tokens  map[string]*core.AccessToken // id -> token
	hashIdx map[string]string            // hash -> tokenID
	updates []*core.AccessToken
}

func newMockRepo() *mockAccessTokenRepo {
	return &mockAccessTokenRepo{
		tokens:  make(map[string]*core.AccessToken),
		hashIdx: make(map[string]string),
	}
}

func (m *mockAccessTokenRepo) Create(token *core.AccessToken) error {
	m.tokens[token.ID] = token
	if token.TokenHash != "" {
		m.hashIdx[token.TokenHash] = token.ID
	}
	return nil
}

func (m *mockAccessTokenRepo) GetByID(id string) (*core.AccessToken, error) {
	t, ok := m.tokens[id]
	if !ok {
		return nil, core.ErrNotFound
	}
	return t, nil
}

func (m *mockAccessTokenRepo) GetByHash(hash string) (*core.AccessToken, error) {
	id, ok := m.hashIdx[hash]
	if !ok {
		return nil, core.ErrNotFound
	}
	return m.GetByID(id)
}

func (m *mockAccessTokenRepo) ListByUser(userID string) ([]*core.AccessToken, error) {
	var result []*core.AccessToken
	for _, t := range m.tokens {
		if t.UserID == userID {
			result = append(result, t)
		}
	}
	return result, nil
}

func (m *mockAccessTokenRepo) Update(token *core.AccessToken) error {
	m.updates = append(m.updates, token)
	m.tokens[token.ID] = token
	return nil
}

func (m *mockAccessTokenRepo) Delete(id string) error {
	t, ok := m.tokens[id]
	if ok {
		delete(m.hashIdx, t.TokenHash)
		delete(m.tokens, id)
	}
	return nil
}

// mockRepoWithUpdateError wraps mockAccessTokenRepo but returns error on Update.
type mockRepoWithUpdateError struct {
	*mockAccessTokenRepo
}

func (m *mockRepoWithUpdateError) Update(token *core.AccessToken) error {
	return fmt.Errorf("db connection lost")
}

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

func makeActiveToken(id, userID, rawToken string) *core.AccessToken {
	hash := storage.GenerateTokenHash(rawToken)
	now := time.Now().UTC()
	return &core.AccessToken{
		ID:          id,
		UserID:      userID,
		Name:        "test-token",
		TokenPrefix: rawToken[:6],
		TokenHash:   hash,
		Status:      core.AccessTokenActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// 1. Active user token authenticates successfully and updates LastUsedAt.
func TestAuthenticateNodeToken_ActiveUserToken(t *testing.T) {
	repo := newMockRepo()
	tok := makeActiveToken("tok-1", "user-1", "test-raw-token")
	repo.Create(tok)

	svc := NewAccessTokenAuthService(repo, "").(*accessTokenAuthService)
	grant, err := svc.AuthenticateNodeToken(context.Background(), "test-raw-token")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if grant.UserID != "user-1" {
		t.Errorf("UserID = %q, want %q", grant.UserID, "user-1")
	}
	if grant.AccessTokenID != "tok-1" {
		t.Errorf("AccessTokenID = %q, want %q", grant.AccessTokenID, "tok-1")
	}
	if grant.LegacyGlobal {
		t.Error("LegacyGlobal should be false")
	}
	if len(repo.updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(repo.updates))
	}
	if repo.updates[0].LastUsedAt == nil {
		t.Error("LastUsedAt should have been set")
	}
}

// 2. Disabled token is rejected.
func TestAuthenticateNodeToken_DisabledToken(t *testing.T) {
	repo := newMockRepo()
	tok := makeActiveToken("tok-2", "user-2", "disabled-raw")
	tok.Status = core.AccessTokenDisabled
	repo.Create(tok)

	svc := NewAccessTokenAuthService(repo, "").(*accessTokenAuthService)
	grant, err := svc.AuthenticateNodeToken(context.Background(), "disabled-raw")
	if err == nil {
		t.Fatal("expected error for disabled token")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("error should mention 'disabled', got %q", err.Error())
	}
	if grant != nil {
		t.Error("grant should be nil for disabled token")
	}
}

// 3. Legacy global token works when no user tokens exist.
func TestAuthenticateNodeToken_LegacyGlobalToken(t *testing.T) {
	repo := newMockRepo()
	svc := NewAccessTokenAuthService(repo, "legacy-secret").(*accessTokenAuthService)

	grant, err := svc.AuthenticateNodeToken(context.Background(), "legacy-secret")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if grant.UserID != "system" {
		t.Errorf("UserID = %q, want %q", grant.UserID, "system")
	}
	if grant.AccessTokenID != "" {
		t.Errorf("AccessTokenID = %q, want empty", grant.AccessTokenID)
	}
	if !grant.LegacyGlobal {
		t.Error("LegacyGlobal should be true")
	}
}

// 4. When user token hash not found, legacy fallback succeeds.
func TestAuthenticateNodeToken_LegacyFallback_WhenUserTokenNotFound(t *testing.T) {
	repo := newMockRepo()
	// Create a token for a different raw value so the repo is not empty.
	repo.Create(makeActiveToken("tok-other", "user-other", "some-other-raw"))

	svc := NewAccessTokenAuthService(repo, "legacy-secret").(*accessTokenAuthService)
	grant, err := svc.AuthenticateNodeToken(context.Background(), "legacy-secret")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !grant.LegacyGlobal {
		t.Error("expected legacy fallback, LegacyGlobal should be true")
	}
}

// 5. Invalid token with empty legacy returns error.
func TestAuthenticateNodeToken_InvalidToken(t *testing.T) {
	repo := newMockRepo()
	svc := NewAccessTokenAuthService(repo, "").(*accessTokenAuthService)

	grant, err := svc.AuthenticateNodeToken(context.Background(), "garbage")
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
	if !strings.Contains(err.Error(), "invalid token") {
		t.Errorf("error should contain 'invalid token', got %q", err.Error())
	}
	if grant != nil {
		t.Error("grant should be nil")
	}
}

// 6. User token takes priority when raw value equals legacy token.
func TestAuthenticateNodeToken_UserTokenPriority(t *testing.T) {
	repo := newMockRepo()
	// User token whose raw value is the same as the legacy token.
	repo.Create(makeActiveToken("tok-pri", "user-pri", "shared-secret"))

	svc := NewAccessTokenAuthService(repo, "shared-secret").(*accessTokenAuthService)
	grant, err := svc.AuthenticateNodeToken(context.Background(), "shared-secret")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Should match user token, not legacy.
	if grant.UserID != "user-pri" {
		t.Errorf("UserID = %q, want %q (user token should win)", grant.UserID, "user-pri")
	}
	if grant.LegacyGlobal {
		t.Error("LegacyGlobal should be false; user token should take priority")
	}
}

// 7. Update error does not block authentication.
func TestAuthenticateNodeToken_UpdateErrorDoesNotBlock(t *testing.T) {
	base := newMockRepo()
	tok := makeActiveToken("tok-ue", "user-ue", "raw-update-err")
	base.Create(tok)

	repo := &mockRepoWithUpdateError{base}
	svc := NewAccessTokenAuthService(repo, "").(*accessTokenAuthService)

	grant, err := svc.AuthenticateNodeToken(context.Background(), "raw-update-err")
	if err != nil {
		t.Fatalf("expected no error despite update failure, got %v", err)
	}
	if grant == nil {
		t.Fatal("grant should not be nil")
	}
	if grant.UserID != "user-ue" {
		t.Errorf("UserID = %q, want %q", grant.UserID, "user-ue")
	}
}

// 8. GenerateAccessTokenRaw produces correctly formatted tokens.
func TestGenerateAccessTokenRaw_Format(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 10; i++ {
		raw, err := GenerateAccessTokenRaw()
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if !strings.HasPrefix(raw, "mat_") {
			t.Errorf("iteration %d: token %q should start with mat_", i, raw)
		}
		if len(raw) != 68 {
			t.Errorf("iteration %d: len = %d, want 68", i, len(raw))
		}
		suffix := raw[4:]
		for _, c := range suffix {
			if !isHexRune(c) {
				t.Errorf("iteration %d: non-hex char %c in %q", i, c, raw)
				break
			}
		}
		if seen[raw] {
			t.Errorf("iteration %d: duplicate token generated", i)
		}
		seen[raw] = true
	}
}

// 9. Hash of generated token is consistent with sha256.
func TestGenerateAccessTokenRaw_HashConsistency(t *testing.T) {
	raw, err := GenerateAccessTokenRaw()
	if err != nil {
		t.Fatal(err)
	}
	hash := storage.GenerateTokenHash(raw)

	if len(hash) != 64 {
		t.Errorf("hash length = %d, want 64", len(hash))
	}
	// Verify it matches a manual sha256 computation.
	expected := sha256.Sum256([]byte(raw))
	expectedHex := hex.EncodeToString(expected[:])
	if hash != expectedHex {
		t.Errorf("hash mismatch:\n got:  %s\n want: %s", hash, expectedHex)
	}
	for _, c := range hash {
		if !isHexRune(c) {
			t.Errorf("non-hex char %c in hash", c)
			break
		}
	}
}

// isHexRune reports whether r is a lowercase hex digit.
func isHexRune(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')
}
