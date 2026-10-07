package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
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

func (m *mockAccessTokenRepo) ListAll() ([]*core.AccessToken, error) {
	var result []*core.AccessToken
	for _, t := range m.tokens {
		result = append(result, t)
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

func (m *mockAccessTokenRepo) TouchLastUsed(id string, ts time.Time) error {
	t, ok := m.tokens[id]
	if !ok {
		return core.ErrNotFound
	}
	t.LastUsedAt = &ts
	m.updates = append(m.updates, t)
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
	grant, _, err := svc.AuthenticateNodeToken(context.Background(), "test-raw-token")
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
	grant, _, err := svc.AuthenticateNodeToken(context.Background(), "disabled-raw")
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

	grant, _, err := svc.AuthenticateNodeToken(context.Background(), "legacy-secret")
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
	grant, _, err := svc.AuthenticateNodeToken(context.Background(), "legacy-secret")
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

	grant, _, err := svc.AuthenticateNodeToken(context.Background(), "garbage")
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
	grant, _, err := svc.AuthenticateNodeToken(context.Background(), "shared-secret")
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

	grant, _, err := svc.AuthenticateNodeToken(context.Background(), "raw-update-err")
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

// ---------------------------------------------------------------------------
// AuthenticateNodeProof（SEC-01 proof 认证）
// ---------------------------------------------------------------------------

// clientProofHex 按客户端公式计算 proof：h=sha256(token); proof=hex(HMAC(h[:], challenge))
func clientProofHex(t *testing.T, rawToken string, challenge []byte) string {
	t.Helper()
	h := sha256.Sum256([]byte(rawToken))
	mac := hmac.New(sha256.New, h[:])
	mac.Write(challenge)
	return hex.EncodeToString(mac.Sum(nil))
}

// proof 往返：按客户端公式算出的 proof 必须通过认证并返回正确 grant + TouchLastUsed
func TestAuthenticateNodeProof_RoundTrip(t *testing.T) {
	repo := newMockRepo()
	raw := "mat_proofroundtrip0123456789abcdef"
	repo.Create(makeActiveToken("tok-proof-1", "user-proof", raw))

	svc := NewAccessTokenAuthService(repo, "").(*accessTokenAuthService)
	challenge := make([]byte, 32)
	for i := range challenge {
		challenge[i] = byte(i)
	}

	grant, _, err := svc.AuthenticateNodeProof(context.Background(), clientProofHex(t, raw, challenge), challenge)
	if err != nil {
		t.Fatalf("expected proof to authenticate, got %v", err)
	}
	if grant.UserID != "user-proof" || grant.AccessTokenID != "tok-proof-1" {
		t.Errorf("grant mismatch: %+v", grant)
	}
	if grant.LegacyGlobal {
		t.Error("LegacyGlobal should be false for access token proof")
	}
	if len(repo.updates) != 1 || repo.updates[0].LastUsedAt == nil {
		t.Errorf("TouchLastUsed must fire exactly once, updates=%d", len(repo.updates))
	}
}

// 多候选命中第二个：遍历顺序无关，命中的 token 决定 grant
func TestAuthenticateNodeProof_MultipleCandidates(t *testing.T) {
	repo := newMockRepo()
	repo.Create(makeActiveToken("tok-m1", "user-m1", "mat_multicandidate_first00"))
	raw2 := "mat_multicandidate_second0"
	repo.Create(makeActiveToken("tok-m2", "user-m2", raw2))

	svc := NewAccessTokenAuthService(repo, "").(*accessTokenAuthService)
	challenge := []byte("0123456789abcdef0123456789abcdef")

	grant, _, err := svc.AuthenticateNodeProof(context.Background(), clientProofHex(t, raw2, challenge), challenge)
	if err != nil {
		t.Fatalf("expected authentication via second candidate, got %v", err)
	}
	if grant.UserID != "user-m2" || grant.AccessTokenID != "tok-m2" {
		t.Errorf("grant mismatch: %+v", grant)
	}
}

// challenge 改一字节：proof 失效
func TestAuthenticateNodeProof_WrongChallenge(t *testing.T) {
	repo := newMockRepo()
	raw := "mat_wrongchallenge00000000000000"
	repo.Create(makeActiveToken("tok-wc", "user-wc", raw))

	svc := NewAccessTokenAuthService(repo, "").(*accessTokenAuthService)
	challenge := make([]byte, 32)
	rand.Read(challenge) //nolint:errcheck // 测试随机源失败无处理意义
	tampered := append([]byte(nil), challenge...)
	tampered[7] ^= 0x01

	proof := clientProofHex(t, raw, challenge)
	grant, _, err := svc.AuthenticateNodeProof(context.Background(), proof, tampered)
	if err == nil {
		t.Fatal("proof bound to a different challenge must fail")
	}
	if grant != nil {
		t.Error("grant should be nil on failure")
	}
	if !strings.Contains(err.Error(), "invalid token") {
		t.Errorf("error must follow the legacy wording, got %q", err.Error())
	}
}

// 错误 proof（值不对 / 非法 hex / 长度不符）一律失败且不区分原因
func TestAuthenticateNodeProof_BadProofValues(t *testing.T) {
	repo := newMockRepo()
	raw := "mat_badproof0000000000000000000"
	repo.Create(makeActiveToken("tok-bp", "user-bp", raw))
	legacyRaw := "legacy-global-secret"
	svc := NewAccessTokenAuthService(repo, legacyRaw).(*accessTokenAuthService)
	challenge := []byte("fedcba9876543210fedcba9876543210")

	cases := []struct {
		name     string
		proofHex string
	}{
		{"值错误的合法 hex", hex.EncodeToString(make([]byte, 32))},
		{"非 hex 字符", "zz-not-hex-at-all"},
		{"长度不符（31 字节）", hex.EncodeToString(make([]byte, 31))},
		{"空字符串", ""},
	}
	for _, tc := range cases {
		grant, _, err := svc.AuthenticateNodeProof(context.Background(), tc.proofHex, challenge)
		if err == nil || grant != nil {
			t.Errorf("%s: must fail, got grant=%+v err=%v", tc.name, grant, err)
		}
	}
}

// 禁用 token 的 proof 不参与候选
func TestAuthenticateNodeProof_DisabledTokenSkipped(t *testing.T) {
	repo := newMockRepo()
	raw := "mat_disabledproof0000000000000"
	tok := makeActiveToken("tok-dis", "user-dis", raw)
	tok.Status = core.AccessTokenDisabled
	repo.Create(tok)

	svc := NewAccessTokenAuthService(repo, "").(*accessTokenAuthService)
	challenge := []byte("aabbccddeeff00112233445566778899")

	grant, _, err := svc.AuthenticateNodeProof(context.Background(), clientProofHex(t, raw, challenge), challenge)
	if err == nil {
		t.Fatalf("disabled token proof must fail, got grant %+v", grant)
	}
	if len(repo.updates) != 0 {
		t.Errorf("TouchLastUsed must not fire for disabled token, updates=%d", len(repo.updates))
	}
}

// legacy 全局 token 的 proof：返回 LegacyGlobal grant
func TestAuthenticateNodeProof_LegacyGlobal(t *testing.T) {
	repo := newMockRepo()
	repo.Create(makeActiveToken("tok-lg", "user-lg", "mat_legacycoexist0000000000"))
	legacyRaw := "the-legacy-global-token"

	svc := NewAccessTokenAuthService(repo, legacyRaw).(*accessTokenAuthService)
	challenge := make([]byte, 32)
	rand.Read(challenge) //nolint:errcheck

	grant, _, err := svc.AuthenticateNodeProof(context.Background(), clientProofHex(t, legacyRaw, challenge), challenge)
	if err != nil {
		t.Fatalf("legacy proof must authenticate, got %v", err)
	}
	if grant.UserID != "system" || grant.AccessTokenID != "" || !grant.LegacyGlobal {
		t.Errorf("legacy grant mismatch: %+v", grant)
	}
	if len(repo.updates) != 0 {
		t.Errorf("legacy proof must not TouchLastUsed on access tokens, updates=%d", len(repo.updates))
	}
}

// 无任何候选命中（空库无 legacy）→ 与旧路径同文案错误
func TestAuthenticateNodeProof_NoCandidates(t *testing.T) {
	repo := newMockRepo()
	svc := NewAccessTokenAuthService(repo, "").(*accessTokenAuthService)

	grant, _, err := svc.AuthenticateNodeProof(context.Background(), hex.EncodeToString(make([]byte, 32)), []byte("challenge-bytes-32..............."))
	if err == nil || grant != nil {
		t.Fatalf("must fail with no candidates, got grant=%+v err=%v", grant, err)
	}
	if !strings.Contains(err.Error(), "invalid token") {
		t.Errorf("error must follow the legacy wording, got %q", err.Error())
	}
}

// ---------------------------------------------------------------------------
// psk 返回（方案 B：AuthenticateNodeProof/Token 匹配成功时携带 sha256(token)）
// ---------------------------------------------------------------------------

// proof 命中用户级 token：psk 必须等于 sha256(明文) 的 32 字节原始值（Noise XXpsk2 用）
func TestAuthenticateNodeProof_ReturnsPSK(t *testing.T) {
	repo := newMockRepo()
	raw := "mat_pskreturn000000000000000000"
	repo.Create(makeActiveToken("tok-psk", "user-psk", raw))

	svc := NewAccessTokenAuthService(repo, "").(*accessTokenAuthService)
	challenge := []byte("psk-return-challenge-32-bytes-ok!!")

	grant, psk, err := svc.AuthenticateNodeProof(context.Background(), clientProofHex(t, raw, challenge), challenge)
	if err != nil {
		t.Fatalf("proof must authenticate, got %v", err)
	}
	if grant == nil {
		t.Fatal("grant must not be nil")
	}
	want := sha256.Sum256([]byte(raw))
	if len(psk) != 32 || !bytes.Equal(psk, want[:]) {
		t.Fatalf("psk mismatch: got %x (%d bytes), want %x", psk, len(psk), want)
	}
}

// proof 命中 legacy 候选：psk = sha256(legacy 明文) 原始字节
func TestAuthenticateNodeProof_LegacyReturnsPSK(t *testing.T) {
	repo := newMockRepo()
	legacyRaw := "legacy-psk-raw-token"
	svc := NewAccessTokenAuthService(repo, legacyRaw).(*accessTokenAuthService)
	challenge := []byte("legacy-psk-challenge-32-bytes-ok!!")

	_, psk, err := svc.AuthenticateNodeProof(context.Background(), clientProofHex(t, legacyRaw, challenge), challenge)
	if err != nil {
		t.Fatalf("legacy proof must authenticate, got %v", err)
	}
	want := sha256.Sum256([]byte(legacyRaw))
	if !bytes.Equal(psk, want[:]) {
		t.Fatalf("legacy psk mismatch: got %x, want %x", psk, want)
	}
}

// 认证失败：psk 必须为 nil（绝不部分返回）
func TestAuthenticateNodeProof_FailureReturnsNilPSK(t *testing.T) {
	repo := newMockRepo()
	svc := NewAccessTokenAuthService(repo, "").(*accessTokenAuthService)

	grant, psk, err := svc.AuthenticateNodeProof(context.Background(), hex.EncodeToString(make([]byte, 32)), []byte("challenge-bytes-32..............."))
	if err == nil || grant != nil {
		t.Fatalf("must fail, got grant=%+v err=%v", grant, err)
	}
	if psk != nil {
		t.Fatalf("psk must be nil on failure, got %x", psk)
	}
}

// 明文 token 认证路径（用户级 token 命中与 legacy 全局 token 兜底）psk 一律为
// nil（B.2/F1：本路径 token 明文上线，窃听者可自行推导 sha256(token)，服务端
// 供给 psk 只会产出假加密与 enc=true 审计假信号；psk 仅 proof 路径产出）
func TestAuthenticateNodeToken_PlaintextPathsReturnNilPSK(t *testing.T) {
	repo := newMockRepo()
	raw := "mat_tokenpsk00000000000000000000"
	repo.Create(makeActiveToken("tok-tpsk", "user-tpsk", raw))

	svc := NewAccessTokenAuthService(repo, "").(*accessTokenAuthService)
	grant, psk, err := svc.AuthenticateNodeToken(context.Background(), raw)
	if err != nil {
		t.Fatalf("token must authenticate, got %v", err)
	}
	if grant == nil {
		t.Fatal("grant must not be nil")
	}
	if psk != nil {
		t.Fatalf("plaintext token path must return nil psk (B.2 F1: token goes on the wire), got %x", psk)
	}

	// legacy 明文命中：同样 psk=nil
	legacyRaw := "legacy-token-psk"
	svc2 := NewAccessTokenAuthService(repo, legacyRaw).(*accessTokenAuthService)
	_, psk2, err := svc2.AuthenticateNodeToken(context.Background(), legacyRaw)
	if err != nil {
		t.Fatalf("legacy token must authenticate, got %v", err)
	}
	if psk2 != nil {
		t.Fatalf("legacy plaintext token path must return nil psk, got %x", psk2)
	}

	// 失败路径 psk 为 nil
	_, psk3, err := svc.AuthenticateNodeToken(context.Background(), "no-such-token")
	if err == nil || psk3 != nil {
		t.Fatalf("failure must return nil psk, got psk=%x err=%v", psk3, err)
	}
}
