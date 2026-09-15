package service

import (
	"context"
	"crypto/rand"
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

// AuthenticateNodeToken 校验接入 token，返回认证结果
func (s *accessTokenAuthService) AuthenticateNodeToken(ctx context.Context, rawToken string) (*core.NodeAccessGrant, error) {
	// 1. 先尝试用户级 token
	if rawToken != "" {
		hash := storage.GenerateTokenHash(rawToken)
		token, err := s.tokenRepo.GetByHash(hash)
		if err == nil && token != nil {
			if token.Status != core.AccessTokenActive {
				slog.Warn("Access token disabled", "tokenId", token.ID, "userId", token.UserID)
				return nil, fmt.Errorf("access token disabled")
			}
			// 更新 last_used_at：必须走 TouchLastUsed（重读最新记录只改该字段）。
			// 用旧快照整记录回写会与并发禁用/轮换交错，把已吊销 token 无声复活
			if updateErr := s.tokenRepo.TouchLastUsed(token.ID, time.Now().UTC()); updateErr != nil {
				slog.Warn("Failed to update token last_used_at", "tokenId", token.ID, "error", updateErr)
			}
			slog.Info("Node authenticated via access token", "tokenId", token.ID, "userId", token.UserID)
			return &core.NodeAccessGrant{
				UserID:        token.UserID,
				AccessTokenID: token.ID,
				LegacyGlobal:  false,
			}, nil
		}
	}

	// 2. 兼容旧全局 token（使用 constant-time 比较）
	if s.legacyToken != "" && subtle.ConstantTimeCompare([]byte(rawToken), []byte(s.legacyToken)) == 1 {
		slog.Info("Node authenticated via legacy global token")
		return &core.NodeAccessGrant{
			UserID:        "system",
			AccessTokenID: "",
			LegacyGlobal:  true,
		}, nil
	}

	return nil, fmt.Errorf("invalid token")
}

// GenerateAccessTokenRaw 生成 token 明文：mat_ 前缀 + 32 字节 hex
func GenerateAccessTokenRaw() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "mat_" + hex.EncodeToString(b), nil
}
