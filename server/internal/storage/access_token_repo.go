package storage

import (
	"errors"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nalgeon/redka"

	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/crypto"
)

type accessTokenRepo struct {
	db  *redka.DB
	mu  sync.Mutex // 串行化整记录读改写：并发 Update/TouchLastUsed 交错会复活已禁用/已轮换的 token
}

func (r *accessTokenRepo) TouchLastUsed(id string, ts time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 重读最新记录后只改 last_used_at：绝不能用调用方旧快照整记录回写，
	// 否则与并发禁用/轮换交错时会把 Status/TokenHash 回滚到旧值（吊销旁路）
	fresh, err := r.GetByID(id)
	if err != nil {
		return err
	}
	fresh.LastUsedAt = &ts
	return r.updateLocked(fresh)
}

func (r *accessTokenRepo) saveTokenRecord(token *core.AccessToken) error {
	data, err := json.Marshal(token)
	if err != nil {
		return fmt.Errorf("marshal access token: %w", err)
	}
	if _, err := r.db.Hash().Set("access_tokens", token.ID, string(data)); err != nil {
		return fmt.Errorf("save access token: %w", err)
	}
	return nil
}

// NewAccessTokenRepo 创建接入 Token 仓库
func NewAccessTokenRepo(db *redka.DB) core.AccessTokenRepo {
	return &accessTokenRepo{db: db}
}

func (r *accessTokenRepo) Create(token *core.AccessToken) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.saveTokenRecord(token); err != nil {
		return err
	}
	// 建立 hash 索引
	if token.TokenHash != "" {
		if _, err := r.db.Hash().Set("access_token_hash_index", token.TokenHash, token.ID); err != nil {
			if _, rollbackErr := r.db.Hash().Delete("access_tokens", token.ID); rollbackErr != nil {
				slog.Error("Failed to rollback access token create after index write failure",
					"tokenId", token.ID, "error", rollbackErr)
			}
			return fmt.Errorf("save token hash index: %w", err)
		}
	}
	return nil
}

func (r *accessTokenRepo) GetByID(id string) (*core.AccessToken, error) {
	val, err := r.db.Hash().Get("access_tokens", id)
	if err != nil {
		if errors.Is(err, redka.ErrNotFound) {
			return nil, core.ErrNotFound
		}
		return nil, fmt.Errorf("read access token %s: %w", id, err)
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
		if errors.Is(err, redka.ErrNotFound) {
			return nil, core.ErrNotFound
		}
		return nil, fmt.Errorf("read access token hash index: %w", err)
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

// ListAll 全量列举全部 access token（跨用户，供 proof 认证遍历候选）
func (r *accessTokenRepo) ListAll() ([]*core.AccessToken, error) {
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
		tokens = append(tokens, &token)
	}
	return tokens, nil
}

func (r *accessTokenRepo) Update(token *core.AccessToken) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.updateLocked(token)
}

// updateLocked 要求已持 r.mu：Update 与 TouchLastUsed 的整记录读改写必须互斥
func (r *accessTokenRepo) updateLocked(token *core.AccessToken) error {
	oldToken, err := r.GetByID(token.ID)
	if err != nil {
		return err
	}

	token.UpdatedAt = time.Now().UTC()
	hashChanged := oldToken.TokenHash != token.TokenHash

	if hashChanged && token.TokenHash != "" {
		if _, err := r.db.Hash().Set("access_token_hash_index", token.TokenHash, token.ID); err != nil {
			return fmt.Errorf("save new token hash index: %w", err)
		}
	}

	if err := r.saveTokenRecord(token); err != nil {
		if hashChanged && token.TokenHash != "" {
			if _, rollbackErr := r.db.Hash().Delete("access_token_hash_index", token.TokenHash); rollbackErr != nil {
				slog.Error("Failed to rollback new token hash index after record update failure",
					"tokenId", token.ID, "error", rollbackErr)
			}
		}
		return err
	}

	if hashChanged && oldToken.TokenHash != "" {
		if _, err := r.db.Hash().Delete("access_token_hash_index", oldToken.TokenHash); err != nil {
			if rollbackErr := r.saveTokenRecord(oldToken); rollbackErr != nil {
				slog.Error("Failed to rollback access token record after old index delete failure",
					"tokenId", token.ID, "error", rollbackErr)
			}
			if hashChanged && token.TokenHash != "" {
				if _, cleanupErr := r.db.Hash().Delete("access_token_hash_index", token.TokenHash); cleanupErr != nil {
					slog.Error("Failed to cleanup new token hash index after old index delete failure",
						"tokenId", token.ID, "error", cleanupErr)
				}
			}
			return fmt.Errorf("delete old token hash index: %w", err)
		}
	}
	return nil
}

func (r *accessTokenRepo) Delete(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	// 先获取 token 以便清理 hash 索引
	token, err := r.GetByID(id)
	if err == nil && token.TokenHash != "" {
		if _, delErr := r.db.Hash().Delete("access_token_hash_index", token.TokenHash); delErr != nil {
			return fmt.Errorf("delete token hash index: %w", delErr)
		}
	}
	if _, err = r.db.Hash().Delete("access_tokens", id); err != nil {
		if token != nil && token.TokenHash != "" {
			if _, restoreErr := r.db.Hash().Set("access_token_hash_index", token.TokenHash, id); restoreErr != nil {
				slog.Error("Failed to restore token hash index after delete rollback",
					"tokenId", id, "error", restoreErr)
			}
		}
		return err
	}
	return nil
}

// GenerateTokenHash 计算 token 明文的 sha256 hex。
// 实现已下沉 internal/crypto（架构审查 🔴5：消除 api→storage 跨层直连），
// 此处保留导出名使包内与既有调用方零改动。
func GenerateTokenHash(rawToken string) string {
	return crypto.GenerateTokenHash(rawToken)
}
