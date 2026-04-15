package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nalgeon/redka"

	"moleAgent_Serv/internal/core"
)

type accessTokenRepo struct {
	db *redka.DB
}

// NewAccessTokenRepo 创建接入 Token 仓库
func NewAccessTokenRepo(db *redka.DB) core.AccessTokenRepo {
	return &accessTokenRepo{db: db}
}

func (r *accessTokenRepo) Create(token *core.AccessToken) error {
	data, err := json.Marshal(token)
	if err != nil {
		return fmt.Errorf("marshal access token: %w", err)
	}
	if _, err := r.db.Hash().Set("access_tokens", token.ID, string(data)); err != nil {
		return fmt.Errorf("save access token: %w", err)
	}
	// 建立 hash 索引
	if token.TokenHash != "" {
		if _, err := r.db.Hash().Set("access_token_hash_index", token.TokenHash, token.ID); err != nil {
			return fmt.Errorf("save token hash index: %w", err)
		}
	}
	return nil
}

func (r *accessTokenRepo) GetByID(id string) (*core.AccessToken, error) {
	val, err := r.db.Hash().Get("access_tokens", id)
	if err != nil {
		return nil, core.ErrNotFound
	}
	if val.String() == "" {
		return nil, core.ErrNotFound
	}
	var token core.AccessToken
	if err := json.Unmarshal([]byte(val.String()), &token); err != nil {
		return nil, err
	}
	return &token, nil
}

func (r *accessTokenRepo) GetByHash(hash string) (*core.AccessToken, error) {
	tokenIDVal, err := r.db.Hash().Get("access_token_hash_index", hash)
	if err != nil {
		return nil, core.ErrNotFound
	}
	tokenID := tokenIDVal.String()
	if tokenID == "" {
		return nil, core.ErrNotFound
	}
	return r.GetByID(tokenID)
}

func (r *accessTokenRepo) ListByUser(userID string) ([]*core.AccessToken, error) {
	items, err := r.db.Hash().Items("access_tokens")
	if err != nil {
		return nil, err
	}
	var tokens []*core.AccessToken
	for _, v := range items {
		var token core.AccessToken
		if err := json.Unmarshal([]byte(v.String()), &token); err != nil {
			continue
		}
		if token.UserID == userID {
			tokens = append(tokens, &token)
		}
	}
	return tokens, nil
}

func (r *accessTokenRepo) Update(token *core.AccessToken) error {
	token.UpdatedAt = time.Now().UTC()
	data, err := json.Marshal(token)
	if err != nil {
		return err
	}
	_, err = r.db.Hash().Set("access_tokens", token.ID, string(data))
	return err
}

func (r *accessTokenRepo) Delete(id string) error {
	// 先获取 token 以便清理 hash 索引
	token, err := r.GetByID(id)
	if err == nil && token.TokenHash != "" {
		if _, delErr := r.db.Hash().Delete("access_token_hash_index", token.TokenHash); delErr != nil {
			slog.Warn("Failed to clean up token hash index", "tokenId", id, "error", delErr)
		}
	}
	_, err = r.db.Hash().Delete("access_tokens", id)
	return err
}

// GenerateTokenHash 计算 token 明文的 sha256 hex
func GenerateTokenHash(rawToken string) string {
	h := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(h[:])
}
