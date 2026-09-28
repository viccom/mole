package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"moleAgent_Serv/internal/auth"
	"moleAgent_Serv/internal/core"
)

// --- mock repo ---

type mockAccessTokenRepo struct {
	tokens  map[string]*core.AccessToken
	hashIdx map[string]string // hash → tokenID
	updates []*core.AccessToken
}

func newMockAccessTokenRepo() *mockAccessTokenRepo {
	return &mockAccessTokenRepo{
		tokens:  make(map[string]*core.AccessToken),
		hashIdx: make(map[string]string),
	}
}

func (m *mockAccessTokenRepo) Create(t *core.AccessToken) error {
	c := *t
	m.tokens[t.ID] = &c
	if t.TokenHash != "" {
		m.hashIdx[t.TokenHash] = t.ID
	}
	return nil
}

func (m *mockAccessTokenRepo) GetByID(id string) (*core.AccessToken, error) {
	t, ok := m.tokens[id]
	if !ok {
		return nil, core.ErrNotFound
	}
	c := *t
	return &c, nil
}

func (m *mockAccessTokenRepo) TouchLastUsed(id string, ts time.Time) error {
	if t, ok := m.tokens[id]; ok {
		t.LastUsedAt = &ts
	}
	return nil
}

func (m *mockAccessTokenRepo) GetByHash(hash string) (*core.AccessToken, error) {
	id, ok := m.hashIdx[hash]
	if !ok {
		return nil, core.ErrNotFound
	}
	return m.GetByID(id)
}

func (m *mockAccessTokenRepo) ListByUser(userID string) ([]*core.AccessToken, error) {
	var out []*core.AccessToken
	for _, t := range m.tokens {
		if t.UserID == userID {
			c := *t
			out = append(out, &c)
		}
	}
	return out, nil
}

func (m *mockAccessTokenRepo) ListAll() ([]*core.AccessToken, error) {
	var out []*core.AccessToken
	for _, t := range m.tokens {
		c := *t
		out = append(out, &c)
	}
	return out, nil
}

func (m *mockAccessTokenRepo) Update(t *core.AccessToken) error {
	c := *t
	m.updates = append(m.updates, &c)
	m.tokens[t.ID] = &c
	if t.TokenHash != "" {
		m.hashIdx[t.TokenHash] = t.ID
	}
	return nil
}

func (m *mockAccessTokenRepo) Delete(id string) error {
	if t, ok := m.tokens[id]; ok {
		delete(m.hashIdx, t.TokenHash)
		delete(m.tokens, id)
	}
	return nil
}

// --- helpers ---

func newTestAccessToken(userID, name string) *core.AccessToken {
	now := time.Now().UTC()
	return &core.AccessToken{
		ID:          fmt.Sprintf("atk_%s_%d", name, now.UnixNano()),
		UserID:      userID,
		Name:        name,
		TokenPrefix: "mat_abcd",
		TokenHash:   fmt.Sprintf("hash_%s_%s", userID, name),
		Status:      core.AccessTokenActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func reqWithClaims(method, path string, body []byte, claims *core.Claims) *http.Request {
	var br *bytes.Reader
	if body != nil {
		br = bytes.NewReader(body)
	} else {
		br = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, br)
	if claims != nil {
		ctx := auth.SetClaims(r.Context(), claims)
		r = r.WithContext(ctx)
	}
	return r
}

func parseResponse(t *testing.T, w *httptest.ResponseRecorder) core.ApiResponse {
	t.Helper()
	var resp core.ApiResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v, body=%s", err, w.Body.String())
	}
	return resp
}

// --- tests ---

func TestAccessTokenHandler_List_Success(t *testing.T) {
	repo := newMockAccessTokenRepo()
	tok1 := newTestAccessToken("userA", "token1")
	tok2 := newTestAccessToken("userA", "token2")
	repo.Create(tok1)
	repo.Create(tok2)

	h := NewAccessTokenHandler(repo)
	claims := &core.Claims{UserID: "userA", Roles: []string{"operator"}}
	req := reqWithClaims(http.MethodGet, "/api/v1/me/access-tokens", nil, claims)
	w := httptest.NewRecorder()

	h.List(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected data type: %T", resp.Data)
	}
	items, _ := data["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if total, _ := data["total"].(float64); total != 2 {
		t.Fatalf("expected total=2, got %v", total)
	}
	// 验证不含 TokenHash
	body := w.Body.String()
	if bytes.Contains(w.Body.Bytes(), []byte("token_hash")) {
		t.Fatal("response should not contain token_hash")
	}
	_ = body
}

func TestAccessTokenHandler_List_NoClaims_401(t *testing.T) {
	repo := newMockAccessTokenRepo()
	h := NewAccessTokenHandler(repo)
	req := reqWithClaims(http.MethodGet, "/api/v1/me/access-tokens", nil, nil)
	w := httptest.NewRecorder()

	h.List(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestAccessTokenHandler_List_OnlyOwnTokens(t *testing.T) {
	repo := newMockAccessTokenRepo()
	repo.Create(newTestAccessToken("userA", "a1"))
	repo.Create(newTestAccessToken("userA", "a2"))
	repo.Create(newTestAccessToken("userB", "b1"))

	h := NewAccessTokenHandler(repo)
	claims := &core.Claims{UserID: "userA", Roles: []string{"operator"}}
	req := reqWithClaims(http.MethodGet, "/api/v1/me/access-tokens", nil, claims)
	w := httptest.NewRecorder()

	h.List(w, req)

	resp := parseResponse(t, w)
	data, _ := resp.Data.(map[string]any)
	items, _ := data["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("expected 2 items for userA, got %d", len(items))
	}
}

func TestAccessTokenHandler_Create_Success(t *testing.T) {
	repo := newMockAccessTokenRepo()
	h := NewAccessTokenHandler(repo)
	claims := &core.Claims{UserID: "userA", Roles: []string{"operator"}}
	body, _ := json.Marshal(map[string]string{"name": "my-token"})
	req := reqWithClaims(http.MethodPost, "/api/v1/me/access-tokens", body, claims)
	w := httptest.NewRecorder()

	h.Create(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected data type: %T", resp.Data)
	}
	if _, hasID := data["id"]; !hasID {
		t.Fatal("response missing 'id'")
	}
	token, _ := data["token"].(string)
	if len(token) < 10 {
		t.Fatalf("token too short: %s", token)
	}
	if token[:4] != "mat_" {
		t.Fatalf("token should start with mat_, got %s", token[:4])
	}
	name, _ := data["name"].(string)
	if name != "my-token" {
		t.Fatalf("expected name=my-token, got %s", name)
	}
	// 验证 repo 中有记录
	if len(repo.tokens) != 1 {
		t.Fatalf("expected 1 token in repo, got %d", len(repo.tokens))
	}
	for _, tok := range repo.tokens {
		if tok.UserID != "userA" {
			t.Fatalf("expected UserID=userA, got %s", tok.UserID)
		}
		if tok.TokenHash == "" {
			t.Fatal("TokenHash should not be empty")
		}
	}
}

func TestAccessTokenHandler_Create_NoClaims_401(t *testing.T) {
	repo := newMockAccessTokenRepo()
	h := NewAccessTokenHandler(repo)
	body, _ := json.Marshal(map[string]string{"name": "test"})
	req := reqWithClaims(http.MethodPost, "/api/v1/me/access-tokens", body, nil)
	w := httptest.NewRecorder()

	h.Create(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestAccessTokenHandler_Create_EmptyName_400(t *testing.T) {
	repo := newMockAccessTokenRepo()
	h := NewAccessTokenHandler(repo)
	claims := &core.Claims{UserID: "userA"}
	body, _ := json.Marshal(map[string]string{"name": ""})
	req := reqWithClaims(http.MethodPost, "/api/v1/me/access-tokens", body, claims)
	w := httptest.NewRecorder()

	h.Create(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestAccessTokenHandler_Create_InvalidBody_400(t *testing.T) {
	repo := newMockAccessTokenRepo()
	h := NewAccessTokenHandler(repo)
	claims := &core.Claims{UserID: "userA"}
	req := reqWithClaims(http.MethodPost, "/api/v1/me/access-tokens", []byte("not-json"), claims)
	w := httptest.NewRecorder()

	h.Create(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestAccessTokenHandler_Delete_Success(t *testing.T) {
	repo := newMockAccessTokenRepo()
	tok := newTestAccessToken("userA", "del-me")
	repo.Create(tok)

	h := NewAccessTokenHandler(repo)
	claims := &core.Claims{UserID: "userA"}
	req := reqWithClaims(http.MethodDelete, "/api/v1/me/access-tokens/"+tok.ID, nil, claims)
	w := httptest.NewRecorder()

	h.Delete(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}
	if _, ok := repo.tokens[tok.ID]; ok {
		t.Fatal("token should be deleted from repo")
	}
}

func TestAccessTokenHandler_Delete_NoClaims_401(t *testing.T) {
	repo := newMockAccessTokenRepo()
	h := NewAccessTokenHandler(repo)
	req := reqWithClaims(http.MethodDelete, "/api/v1/me/access-tokens/atk_xxx", nil, nil)
	w := httptest.NewRecorder()

	h.Delete(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestAccessTokenHandler_Delete_NotFound_404(t *testing.T) {
	repo := newMockAccessTokenRepo()
	h := NewAccessTokenHandler(repo)
	claims := &core.Claims{UserID: "userA"}
	req := reqWithClaims(http.MethodDelete, "/api/v1/me/access-tokens/nonexistent", nil, claims)
	w := httptest.NewRecorder()

	h.Delete(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestAccessTokenHandler_Delete_WrongOwner_404(t *testing.T) {
	repo := newMockAccessTokenRepo()
	tok := newTestAccessToken("userA", "private")
	repo.Create(tok)

	h := NewAccessTokenHandler(repo)
	claims := &core.Claims{UserID: "userB"} // 不同用户
	req := reqWithClaims(http.MethodDelete, "/api/v1/me/access-tokens/"+tok.ID, nil, claims)
	w := httptest.NewRecorder()

	h.Delete(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d, body=%s", w.Code, w.Body.String())
	}
	if _, ok := repo.tokens[tok.ID]; !ok {
		t.Fatal("token should NOT be deleted by non-owner")
	}
}

func TestAccessTokenHandler_Rotate_Success(t *testing.T) {
	repo := newMockAccessTokenRepo()
	tok := newTestAccessToken("userA", "rotate-me")
	originalHash := tok.TokenHash
	repo.Create(tok)

	h := NewAccessTokenHandler(repo)
	claims := &core.Claims{UserID: "userA"}
	req := reqWithClaims(http.MethodPost, "/api/v1/me/access-tokens/"+tok.ID+"/rotate", nil, claims)
	w := httptest.NewRecorder()

	h.Rotate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	data, _ := resp.Data.(map[string]any)

	newToken, _ := data["token"].(string)
	if newToken == "" {
		t.Fatal("rotate should return new token")
	}
	if newToken[:4] != "mat_" {
		t.Fatalf("token should start with mat_, got %s", newToken[:4])
	}

	// 验证 hash 已更新
	updated, _ := repo.GetByID(tok.ID)
	if updated.TokenHash == originalHash {
		t.Fatal("TokenHash should have changed after rotate")
	}
}

func TestAccessTokenHandler_Rotate_NoClaims_401(t *testing.T) {
	repo := newMockAccessTokenRepo()
	h := NewAccessTokenHandler(repo)
	req := reqWithClaims(http.MethodPost, "/api/v1/me/access-tokens/atk_xxx/rotate", nil, nil)
	w := httptest.NewRecorder()

	h.Rotate(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestAccessTokenHandler_Rotate_WrongOwner_404(t *testing.T) {
	repo := newMockAccessTokenRepo()
	tok := newTestAccessToken("userA", "secret")
	repo.Create(tok)

	h := NewAccessTokenHandler(repo)
	claims := &core.Claims{UserID: "userB"}
	req := reqWithClaims(http.MethodPost, "/api/v1/me/access-tokens/"+tok.ID+"/rotate", nil, claims)
	w := httptest.NewRecorder()

	h.Rotate(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d, body=%s", w.Code, w.Body.String())
	}
}
