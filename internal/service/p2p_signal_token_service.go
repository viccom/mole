package service

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
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
	// issueMu 串行化签发：FindByOwner→Upsert 必须原子，否则并发签发会让
	// owner 索引指向新 tokenID 而旧 token 的记录+secret 索引残留为
	// 吊销不到的孤儿凭据（审查 #4）。签发低频，互斥无性能影响。
	issueMu sync.Mutex
}

// NewP2PSignalTokenService 创建签发服务
func NewP2PSignalTokenService(repo core.P2PSignalTokenRepo) *P2PSignalTokenService {
	return &P2PSignalTokenService{repo: repo, now: func() time.Time { return time.Now().UTC() }}
}

// IssueP2PSignalToken 按 (nodeID, tunnelName) 签发：已有记录则复用 tokenID、
// 轮换 secret；返回 (username, password, expiresAtUnix, error)。
func (s *P2PSignalTokenService) IssueP2PSignalToken(nodeID, tunnelName string) (string, string, int64, error) {
	s.issueMu.Lock()
	defer s.issueMu.Unlock()

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
	existing, err := s.repo.FindByOwner(nodeID, tunnelName)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		// 真 DB 错误必须显式失败：当「无记录」继续签发会覆盖 owner 索引，
		// 把旧 token 变成吊销不到的孤儿
		return "", "", 0, fmt.Errorf("find existing p2p signal token: %w", err)
	}
	if err == nil && existing != nil {
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

// RevokeP2PSignalToken 删除对应 p2p tunnel 时吊销。
// 不存在（ErrNotFound）视为已吊销；真 DB 错误必须透传——静默成功会让
// 本应「删除即吊销」的凭据带病存活到 TTL（审查 #10）。
func (s *P2PSignalTokenService) RevokeP2PSignalToken(nodeID, tunnelName string) error {
	tok, err := s.repo.FindByOwner(nodeID, tunnelName)
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("find p2p signal token for revoke: %w", err)
	}
	return s.repo.Delete(tok.TokenID)
}

// RevokeNodeP2PTokens 吊销节点全部 p2p 隧道的信令凭据（节点删除级联，审查 #5）。
// 节点删除后 FindByOwner 永远定位不到其凭据，必须在删除时显式吊销；
// 非 p2p 隧道无凭据，忽略。
func (s *P2PSignalTokenService) RevokeNodeP2PTokens(nodeID string, tunnels []core.Tunnel) {
	for _, t := range tunnels {
		if t.Type != core.TunnelTypeP2P {
			continue
		}
		if err := s.RevokeP2PSignalToken(nodeID, t.Name); err != nil {
			slog.Warn("Failed to revoke p2p signal token on node delete", "node", nodeID, "tunnel", t.Name, "error", err)
		}
	}
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
