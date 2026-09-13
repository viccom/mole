package service

import (
	"os"
	"testing"
	"time"

	"github.com/nalgeon/redka"
	_ "modernc.org/sqlite"

	"moleAgent_Serv/internal/storage"
)

// setupP2PTokenDB 独立 redka 实例（不与 mockNodeRepo 测试混用全局 db 变量）
func setupP2PTokenDB(t *testing.T) *redka.DB {
	t.Helper()
	path := t.TempDir() + "/p2ptoken.db"
	opts := redka.Options{DriverName: "sqlite"}
	db, err := redka.Open(path, &opts)
	if err != nil {
		t.Fatalf("open redka: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		os.Remove(path)
	})
	return db
}

func newP2PTokenService(t *testing.T) (*P2PSignalTokenService, *[]time.Time) {
	t.Helper()
	db := setupP2PTokenDB(t)
	repo := storage.NewP2PSignalTokenRepo(db)
	svc := NewP2PSignalTokenService(repo)
	// 时钟注入：测试过期语义
	times := &[]time.Time{time.Now().UTC()}
	svc.now = func() time.Time { return (*times)[len(*times)-1] }
	return svc, times
}

// 签发语义：tokenID 按 (nodeID, tunnelName) 稳定复用；sha256 落库、明文不再现，
// 因此「幂等复用」落地为 username 稳定 + password 每次轮换（客户端每次连接前
// 拉取即始终持有当前有效 secret；轮换使旧 secret 立即失效，泄露面最小）。
func TestIssueP2PSignalToken(t *testing.T) {
	svc, times := newP2PTokenService(t)

	user1, pass1, exp1, err := svc.IssueP2PSignalToken("Node0001", "p2p-a")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if !isP2PSignalUsername(user1) {
		t.Fatalf("username %q lacks p2p-signal: prefix", user1)
	}
	if pass1 == "" || len(pass1) < 32 {
		t.Fatalf("secret too weak: %q", pass1)
	}
	if exp1 <= time.Now().Unix() {
		t.Fatalf("expires_at %d not in the future", exp1)
	}

	// 再签发：tokenID 稳定（username 不变），secret 轮换
	user2, pass2, _, err := svc.IssueP2PSignalToken("Node0001", "p2p-a")
	if err != nil {
		t.Fatalf("second Issue: %v", err)
	}
	if user2 != user1 {
		t.Fatalf("tokenID must be stable: %q vs %q", user1, user2)
	}
	if pass2 == pass1 {
		t.Fatal("secret must rotate on re-issue")
	}

	// 旧 secret 已被轮换失效，新 secret 可通过校验
	if svc.VerifyP2PSignalToken(tokenIDFromUsername(user1), pass1) {
		t.Fatal("rotated secret must no longer verify")
	}
	if !svc.VerifyP2PSignalToken(tokenIDFromUsername(user1), pass2) {
		t.Fatal("current secret must verify")
	}

	// 与其他 (nodeID, tunnelName) 互不影响
	if _, _, _, err := svc.IssueP2PSignalToken("Node0001", "p2p-b"); err != nil {
		t.Fatalf("Issue other tunnel: %v", err)
	}
	if !svc.VerifyP2PSignalToken(tokenIDFromUsername(user1), pass2) {
		t.Fatal("other tunnel issue must not affect this token")
	}

	// 时间注入已被使用（避免 unused 告警）
	_ = times
}

// 过期后 Verify 必须失败（broker 鉴权不放过过期凭据）
func TestVerifyP2PSignalTokenExpired(t *testing.T) {
	svc, times := newP2PTokenService(t)

	_, pass, _, err := svc.IssueP2PSignalToken("Node0001", "p2p-a")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	tokenID := ""
	toks, listErr := svc.repo.ListAll()
	if listErr == nil && len(toks) > 0 {
		tokenID = toks[0].TokenID
	}
	if tokenID == "" {
		t.Fatal("no token persisted")
	}

	*times = append(*times, time.Now().UTC().Add(25*time.Hour))
	if svc.VerifyP2PSignalToken(tokenID, pass) {
		t.Fatal("expired token must not verify")
	}
}

// 吊销：删除对应 p2p tunnel 时调用，Verify 立即失败
func TestRevokeP2PSignalToken(t *testing.T) {
	svc, _ := newP2PTokenService(t)

	user, pass, _, err := svc.IssueP2PSignalToken("Node0001", "p2p-a")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	tokenID := tokenIDFromUsername(user)

	if err := svc.RevokeP2PSignalToken("Node0001", "p2p-a"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if svc.VerifyP2PSignalToken(tokenID, pass) {
		t.Fatal("revoked token must not verify")
	}
	// 吊销后再签发：新 tokenID（旧记录已删除）
	user2, _, _, err := svc.IssueP2PSignalToken("Node0001", "p2p-a")
	if err != nil {
		t.Fatalf("Issue after revoke: %v", err)
	}
	if user2 == user {
		t.Fatal("tokenID must differ after revoke + re-issue")
	}
}

// 错误口令 / 未知 tokenID 一律拒绝
func TestVerifyP2PSignalTokenRejects(t *testing.T) {
	svc, _ := newP2PTokenService(t)

	user, pass, _, err := svc.IssueP2PSignalToken("Node0001", "p2p-a")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	tokenID := tokenIDFromUsername(user)

	if svc.VerifyP2PSignalToken(tokenID, pass+"x") {
		t.Fatal("wrong secret must not verify")
	}
	if svc.VerifyP2PSignalToken("p2p_nonexistent", pass) {
		t.Fatal("unknown tokenID must not verify")
	}
}

// 确认 core.P2PSignalToken 持久化字段齐全（repo 落库往返）
func TestP2PSignalTokenRepoRoundtrip(t *testing.T) {
	svc, _ := newP2PTokenService(t)
	user, pass, exp, err := svc.IssueP2PSignalToken("Node0002", "p2p-r")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	tok, err := svc.repo.FindByOwner("Node0002", "p2p-r")
	if err != nil || tok == nil {
		t.Fatalf("FindByOwner: %v", err)
	}
	if tok.NodeID != "Node0002" || tok.TunnelName != "p2p-r" || tok.ExpiresAt.Unix() != exp {
		t.Fatalf("persisted = %+v exp=%d", tok, exp)
	}
	if tok.SecretHash == pass || tok.SecretHash == "" {
		t.Fatal("secret must be stored as hash, not plaintext")
	}
	_ = user
}
