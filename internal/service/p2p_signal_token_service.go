package service

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"moleAgent_Serv/internal/auth"
	"moleAgent_Serv/internal/core"
)

// P2PSignalUsernamePrefix P2P 信令 MQTT 用户名前缀（定义在 auth 包，broker/auth/service 共用）
const P2PSignalUsernamePrefix = auth.P2PSignalUsernamePrefix

// p2pSignalTokenTTL 凭据有效期
const p2pSignalTokenTTL = 24 * time.Hour

// P2PSignalTokenService P2P 信令凭据签发/校验。
// 安全模型：单一用途凭据，仅授予 nat-exchange/* 读写；sha256 落库、明文只在
// 签发响应出现一次——tokenID 稳定复用（username 不变）、secret 每次签发轮换，
// 配合客户端「每次连接前拉取」始终持有当前有效凭据。
type P2PSignalTokenService struct {
	repo core.P2PSignalTokenRepo
	now  func() time.Time // 测试注入
}

// NewP2PSignalTokenService 创建签发服务
func NewP2PSignalTokenService(repo core.P2PSignalTokenRepo) *P2PSignalTokenService {
	return &P2PSignalTokenService{repo: repo, now: func() time.Time { return time.Now().UTC() }}
}

// IssueP2PSignalToken 按 (nodeID, tunnelName) 签发：已有记录则复用 tokenID、
// 轮换 secret；返回 (username, password, expiresAtUnix, error)。
func (s *P2PSignalTokenService) IssueP2PSignalToken(nodeID, tunnelName string) (string, string, int64, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", "", 0, fmt.Errorf("generate p2p signal secret: %w", err)
	}
	password := hex.EncodeToString(secret)
	now := s.now()
	tok := &core.P2PSignalToken{
		TokenID:    newP2PSignalTokenID(),
		SecretHash: hashP2PSignalSecret(password),
		NodeID:     nodeID,
		TunnelName: tunnelName,
		ExpiresAt:  now.Add(p2pSignalTokenTTL),
		CreatedAt:  now,
	}
	if existing, err := s.repo.FindByOwner(nodeID, tunnelName); err == nil && existing != nil {
		tok.TokenID = existing.TokenID // username 稳定复用
	}
	if err := s.repo.Upsert(tok); err != nil {
		return "", "", 0, err
	}
	return P2PSignalUsernamePrefix + tok.TokenID, password, tok.ExpiresAt.Unix(), nil
}

// VerifyP2PSignalToken broker authHook 校验入口：tokenID 存在、未过期、
// sha256 恒时比对通过
func (s *P2PSignalTokenService) VerifyP2PSignalToken(tokenID, password string) bool {
	if tokenID == "" || password == "" {
		return false
	}
	tok, err := s.repo.GetByID(tokenID)
	if err != nil || tok == nil {
		return false
	}
	if !s.now().Before(tok.ExpiresAt) {
		return false
	}
	sum := sha256.Sum256([]byte(password))
	return subtle.ConstantTimeCompare([]byte(tok.SecretHash), []byte(hex.EncodeToString(sum[:]))) == 1
}

// RevokeP2PSignalToken 删除对应 p2p tunnel 时吊销（不存在视为已吊销）
func (s *P2PSignalTokenService) RevokeP2PSignalToken(nodeID, tunnelName string) error {
	tok, err := s.repo.FindByOwner(nodeID, tunnelName)
	if err != nil || tok == nil {
		return nil
	}
	return s.repo.Delete(tok.TokenID)
}

func isP2PSignalUsername(username string) bool {
	return strings.HasPrefix(username, P2PSignalUsernamePrefix)
}

func tokenIDFromUsername(username string) string {
	return strings.TrimPrefix(username, P2PSignalUsernamePrefix)
}

func hashP2PSignalSecret(password string) string {
	sum := sha256.Sum256([]byte(password))
	return hex.EncodeToString(sum[:])
}

func newP2PSignalTokenID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("p2pt%d", time.Now().UnixNano())
	}
	return "p2pt" + hex.EncodeToString(b)
}
