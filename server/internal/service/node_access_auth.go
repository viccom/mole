package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/storage"
)

// accessTokenAuthService 实现 NodeAccessAuthenticator
type accessTokenAuthService struct {
	tokenRepo  core.AccessTokenRepo
	legacyToken string // 旧全局 token，兼容期使用
}

// NewAccessTokenAuthService 创建节点接入认证服务
func NewAccessTokenAuthService(tokenRepo core.AccessTokenRepo, legacyToken string) core.NodeAccessAuthenticator {
	return &accessTokenAuthService{
		tokenRepo:   tokenRepo,
		legacyToken: legacyToken,
	}
}

// AuthenticateNodeToken 校验接入 token，返回认证结果与 psk。
// psk = sha256(匹配成功的 token 明文) 32 字节（方案 B 通道加密用）；
// legacy 客户端本不发 enc 位，该 psk 实际不可达，仅代码闭环。
// psk 绝不进 grant 结构体、绝不进日志（grant 会被整体打日志）
func (s *accessTokenAuthService) AuthenticateNodeToken(ctx context.Context, rawToken string) (*core.NodeAccessGrant, []byte, error) {
	// 1. 先尝试用户级 token
	if rawToken != "" {
		hash := storage.GenerateTokenHash(rawToken)
		token, err := s.tokenRepo.GetByHash(hash)
		if err == nil && token != nil {
			if token.Status != core.AccessTokenActive {
				slog.Warn("Access token disabled", "tokenId", token.ID, "userId", token.UserID)
				return nil, nil, fmt.Errorf("access token disabled")
			}
			// 更新 last_used_at：必须走 TouchLastUsed（重读最新记录只改该字段）。
			// 用旧快照整记录回写会与并发禁用/轮换交错，把已吊销 token 无声复活
			if updateErr := s.tokenRepo.TouchLastUsed(token.ID, time.Now().UTC()); updateErr != nil {
				slog.Warn("Failed to update token last_used_at", "tokenId", token.ID, "error", updateErr)
			}
			// psk：命中记录的 TokenHash 即 sha256(明文) 的 hex，解码得 32 字节原始值
			psk, pskErr := hex.DecodeString(hash)
			if pskErr != nil || len(psk) != sha256.Size {
				psk = nil // 哈希记录异常（非 32 字节）时按无 psk 处理，不阻断认证
			}
			slog.Info("Node authenticated via access token", "tokenId", token.ID, "userId", token.UserID)
			return &core.NodeAccessGrant{
				UserID:        token.UserID,
				AccessTokenID: token.ID,
				LegacyGlobal:  false,
			}, psk, nil
		}
	}

	// 2. 兼容旧全局 token（使用 constant-time 比较）
	if s.legacyToken != "" && subtle.ConstantTimeCompare([]byte(rawToken), []byte(s.legacyToken)) == 1 {
		key := sha256.Sum256([]byte(s.legacyToken))
		slog.Info("Node authenticated via legacy global token")
		return &core.NodeAccessGrant{
			UserID:        "system",
			AccessTokenID: "",
			LegacyGlobal:  true,
		}, key[:], nil
	}

	return nil, nil, fmt.Errorf("invalid token")
}

// AuthenticateNodeProof 校验 challenge-response proof（SEC-01 新格式）：
// 客户端公式 proof = hex(HMAC-SHA256(key=sha256(token), msg=challenge))，
// 不发送任何标识符；服务端遍历全部 active access token 逐候选复算比对，
// legacy 全局 token 作为追加候选（key = sha256(legacy 明文) 原始字节）。
// 候选数量小，O(n) HMAC 可接受；全部比对走常量时间比较；
// 错误不区分「候选不存在」与「proof 不符」（同旧路径文案，避免探测面）。
// 匹配成功时第二返回值 psk = 该候选 32 字节 key（= sha256(token 明文)，
// 方案 B 通道加密的预共享密钥）；失败为 nil。psk 绝不进 grant、绝不进日志
func (s *accessTokenAuthService) AuthenticateNodeProof(ctx context.Context, proofHex string, challenge []byte) (*core.NodeAccessGrant, []byte, error) {
	proof, err := hex.DecodeString(proofHex)
	if err != nil || len(proof) != sha256.Size {
		return nil, nil, fmt.Errorf("invalid token")
	}

	// 1. 遍历全部 active access token：key = TokenHash 解码出的 32 字节原始值
	// （= sha256(token 明文)，与客户端公式一致）
	tokens, err := s.tokenRepo.ListAll()
	if err != nil {
		slog.Warn("Failed to list access tokens for proof auth", "error", err)
		return nil, nil, fmt.Errorf("invalid token")
	}
	for _, token := range tokens {
		if token.Status != core.AccessTokenActive {
			continue
		}
		key, keyErr := hex.DecodeString(token.TokenHash)
		if keyErr != nil || len(key) != sha256.Size {
			continue
		}
		mac := hmac.New(sha256.New, key)
		mac.Write(challenge)
		if subtle.ConstantTimeCompare(mac.Sum(nil), proof) == 1 {
			// 命中：更新 last_used_at（照旧路径，重读最新记录只改该字段）
			if updateErr := s.tokenRepo.TouchLastUsed(token.ID, time.Now().UTC()); updateErr != nil {
				slog.Warn("Failed to update token last_used_at", "tokenId", token.ID, "error", updateErr)
			}
			slog.Info("Node authenticated via access token proof", "tokenId", token.ID, "userId", token.UserID)
			return &core.NodeAccessGrant{
				UserID:        token.UserID,
				AccessTokenID: token.ID,
				LegacyGlobal:  false,
			}, key, nil
		}
	}

	// 2. legacy 全局 token 追加候选：key = sha256(legacy 明文) 原始字节
	if s.legacyToken != "" {
		key := sha256.Sum256([]byte(s.legacyToken))
		mac := hmac.New(sha256.New, key[:])
		mac.Write(challenge)
		if subtle.ConstantTimeCompare(mac.Sum(nil), proof) == 1 {
			slog.Info("Node authenticated via legacy global token proof")
			return &core.NodeAccessGrant{
				UserID:        "system",
				AccessTokenID: "",
				LegacyGlobal:  true,
			}, key[:], nil
		}
	}

	return nil, nil, fmt.Errorf("invalid token")
}

// GenerateAccessTokenRaw 生成 token 明文：mat_ 前缀 + 32 字节 hex
func GenerateAccessTokenRaw() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "mat_" + hex.EncodeToString(b), nil
}
