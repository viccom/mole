package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/nalgeon/redka"

	"moleAgent_Serv/internal/core"
)

// p2p 信令 Token 存储：与 access_token 同款 hash + 索引模式。
// 记录 hash 按 TokenID；secret 索引（sha256→id）供 broker 鉴权；owner 索引
// （nodeID/tunnelName→id）供幂等签发与吊销定位。
const (
	p2pTokenRecordHash = "p2p_signal_tokens"
	p2pTokenSecretIdx  = "p2p_signal_token_secret_index"
	p2pTokenOwnerIdx   = "p2p_signal_token_owner_index"
)

type p2pSignalTokenRepo struct {
	db *redka.DB
}

// NewP2PSignalTokenRepo 创建 P2P 信令 Token 仓库
func NewP2PSignalTokenRepo(db *redka.DB) core.P2PSignalTokenRepo {
	return &p2pSignalTokenRepo{db: db}
}

func p2pTokenOwnerKey(nodeID, tunnelName string) string {
	return nodeID + "/" + tunnelName
}

func (r *p2pSignalTokenRepo) Upsert(tok *core.P2PSignalToken) error {
	old, _ := r.GetByID(tok.TokenID) // nil = 新建

	data, err := json.Marshal(tok)
	if err != nil {
		return fmt.Errorf("marshal p2p signal token: %w", err)
	}
	if _, err := r.db.Hash().Set(p2pTokenRecordHash, tok.TokenID, string(data)); err != nil {
		return fmt.Errorf("save p2p signal token: %w", err)
	}
	if _, err := r.db.Hash().Set(p2pTokenSecretIdx, tok.SecretHash, tok.TokenID); err != nil {
		r.rollbackRecord(tok.TokenID, old)
		return fmt.Errorf("save p2p token secret index: %w", err)
	}
	if _, err := r.db.Hash().Set(p2pTokenOwnerIdx, p2pTokenOwnerKey(tok.NodeID, tok.TunnelName), tok.TokenID); err != nil {
		r.rollbackRecord(tok.TokenID, old)
		if old == nil || old.SecretHash != tok.SecretHash {
			if _, delErr := r.db.Hash().Delete(p2pTokenSecretIdx, tok.SecretHash); delErr != nil {
				slog.Error("Failed to cleanup p2p token secret index after owner index failure",
					"tokenId", tok.TokenID, "error", delErr)
			}
		}
		return fmt.Errorf("save p2p token owner index: %w", err)
	}
	// 轮换：旧 secret 索引立即清除（旧 secret 不再可校验）
	if old != nil && old.SecretHash != tok.SecretHash {
		if _, err := r.db.Hash().Delete(p2pTokenSecretIdx, old.SecretHash); err != nil {
			slog.Warn("Failed to delete rotated p2p token secret index",
				"tokenId", tok.TokenID, "error", err)
		}
	}
	return nil
}

func (r *p2pSignalTokenRepo) GetByID(id string) (*core.P2PSignalToken, error) {
	val, err := r.db.Hash().Get(p2pTokenRecordHash, id)
	if err != nil {
		// redka 对缺失 key 返回 ErrNotFound；其余才是真 DB 错误——
		// 吊销路径靠这一区分避免把失败静默当成功
		if errors.Is(err, redka.ErrNotFound) {
			return nil, core.ErrNotFound
		}
		return nil, fmt.Errorf("get p2p signal token %q: %w", id, err)
	}
	if val.String() == "" {
		return nil, core.ErrNotFound
	}
	var tok core.P2PSignalToken
	if err := json.Unmarshal([]byte(val.String()), &tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

func (r *p2pSignalTokenRepo) FindByOwner(nodeID, tunnelName string) (*core.P2PSignalToken, error) {
	idVal, err := r.db.Hash().Get(p2pTokenOwnerIdx, p2pTokenOwnerKey(nodeID, tunnelName))
	if err != nil {
		if errors.Is(err, redka.ErrNotFound) {
			return nil, core.ErrNotFound
		}
		return nil, fmt.Errorf("get p2p token owner index: %w", err)
	}
	if idVal.String() == "" {
		return nil, core.ErrNotFound
	}
	return r.GetByID(idVal.String())
}

func (r *p2pSignalTokenRepo) ListAll() ([]*core.P2PSignalToken, error) {
	items, err := r.db.Hash().Items(p2pTokenRecordHash)
	if err != nil {
		return nil, err
	}
	out := make([]*core.P2PSignalToken, 0, len(items))
	for _, v := range items {
		var tok core.P2PSignalToken
		if err := json.Unmarshal([]byte(v.String()), &tok); err != nil {
			continue
		}
		out = append(out, &tok)
	}
	return out, nil
}

func (r *p2pSignalTokenRepo) Delete(id string) error {
	tok, err := r.GetByID(id)
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			return nil // 不存在视为已删除
		}
		return fmt.Errorf("get p2p signal token for delete: %w", err)
	}
	if _, err := r.db.Hash().Delete(p2pTokenRecordHash, id); err != nil {
		return fmt.Errorf("delete p2p signal token: %w", err)
	}
	if _, err := r.db.Hash().Delete(p2pTokenSecretIdx, tok.SecretHash); err != nil {
		slog.Warn("Failed to delete p2p token secret index", "tokenId", id, "error", err)
	}
	if _, err := r.db.Hash().Delete(p2pTokenOwnerIdx, p2pTokenOwnerKey(tok.NodeID, tok.TunnelName)); err != nil {
		slog.Warn("Failed to delete p2p token owner index", "tokenId", id, "error", err)
	}
	return nil
}

// rollbackRecord 索引写失败时尽力恢复旧记录（对齐 access_token_repo 的回滚保护）
func (r *p2pSignalTokenRepo) rollbackRecord(id string, old *core.P2PSignalToken) {
	if old == nil {
		if _, err := r.db.Hash().Delete(p2pTokenRecordHash, id); err != nil {
			slog.Error("Failed to rollback p2p token record", "tokenId", id, "error", err)
		}
		return
	}
	data, err := json.Marshal(old)
	if err != nil {
		slog.Error("Failed to marshal old p2p token for rollback", "tokenId", id, "error", err)
		return
	}
	if _, err := r.db.Hash().Set(p2pTokenRecordHash, id, string(data)); err != nil {
		slog.Error("Failed to restore old p2p token record", "tokenId", id, "error", err)
	}
}
