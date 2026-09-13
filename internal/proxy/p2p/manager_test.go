//go:build p2p

package p2p

import (
	"testing"
	"time"
)

// setupTestManager 返回 connectFn 恒成功的 Manager（每个配置一个独立 mock session）
func setupTestManager(t *testing.T) (*Manager, *sessionRecorder) {
	t.Helper()
	sessions := withFailingConnect(t, 0, nil)
	m := NewManager("10.0.0.1")
	t.Cleanup(m.Close)
	return m, sessions
}

func p2pCfg(room string) P2PConfig {
	return P2PConfig{Room: room, Protocol: "tcp", LocalPort: 18080,
		TargetHost: "127.0.0.1", TargetPort: 8080}
}

// Notify 增量启停：新配置启动 handler、配置消失即停机（Enable=false 不下发=消失）
func TestNotifyAddAndRemove(t *testing.T) {
	m, _ := setupTestManager(t)

	m.OnTunnelUpdate(map[string]P2PConfig{"a": p2pCfg("roomaaaa001")})
	waitFor(t, func() bool {
		_, err := m.Status("a")
		return err == nil
	}, 2*time.Second, "handler 'a' not started")

	m.OnTunnelUpdate(map[string]P2PConfig{}) // 全量分发：消失=停机
	waitFor(t, func() bool {
		_, err := m.Status("a")
		return err == ErrNotFound
	}, 2*time.Second, "handler 'a' not stopped after removal")
}

// 同名 Para 变更 → 重启 handler（对齐 vpn 的变更重启惯例）
func TestNotifyParaChangeRestarts(t *testing.T) {
	m, sessions := setupTestManager(t)

	cfg := p2pCfg("changeroom1")
	m.OnTunnelUpdate(map[string]P2PConfig{"a": cfg})
	waitFor(t, func() bool { return sessions.len() >= 1 }, 2*time.Second, "first handler not started")
	first := sessions.at(0)

	changed := cfg
	changed.LocalPort = 19999
	m.OnTunnelUpdate(map[string]P2PConfig{"a": changed})
	waitFor(t, func() bool { return sessions.len() >= 2 }, 2*time.Second, "handler not restarted on Para change")

	if !first.wasClosed() {
		t.Fatal("old handler session must be closed on restart")
	}
	// 新 handler 以新配置建隧道
	waitFor(t, func() bool { return sessions.at(1).createCount() == 1 }, 2*time.Second, "restarted handler did not create tunnel")
	if calls := sessions.at(1).createCallsForAssert(); calls[0].LocalPort != 19999 {
		t.Fatalf("restarted handler uses stale config: %+v", calls[0])
	}
}

// 未变更的同名配置不得重启（避免无谓断连）
func TestNotifyUnchangedKeepsHandler(t *testing.T) {
	m, sessions := setupTestManager(t)

	cfg := p2pCfg("sameconf01")
	m.OnTunnelUpdate(map[string]P2PConfig{"a": cfg})
	waitFor(t, func() bool { return sessions.len() >= 1 }, 2*time.Second, "handler not started")

	m.OnTunnelUpdate(map[string]P2PConfig{"a": cfg}) // 同 Para 重复分发
	time.Sleep(100 * time.Millisecond)
	if sessions.len() != 1 {
		t.Fatalf("unchanged Para must not restart handler, sessions=%d", sessions.len())
	}
}

// Status 未知隧道返 ErrNotFound
func TestStatusNotFound(t *testing.T) {
	m, _ := setupTestManager(t)
	if _, err := m.Status("nope"); err != ErrNotFound {
		t.Fatalf("Status(unknown) err = %v, want ErrNotFound", err)
	}
}

// Start/Stop 幂等
func TestStartStopIdempotent(t *testing.T) {
	m, _ := setupTestManager(t)

	m.OnTunnelUpdate(map[string]P2PConfig{"a": p2pCfg("startstop01")})
	waitFor(t, func() bool { _, err := m.Status("a"); return err == nil }, 2*time.Second, "not started")

	if err := m.Stop("a"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := m.Stop("a"); err != nil { // 幂等
		t.Fatalf("second Stop: %v", err)
	}
	if err := m.Start("a"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.Start("a"); err != nil { // 幂等
		t.Fatalf("second Start: %v", err)
	}
	if err := m.Start("nope"); err != ErrNotFound {
		t.Fatalf("Start(unknown) = %v, want ErrNotFound", err)
	}
}

// Close 关闭全部 handler 且 Run goroutine 全部退出（不泄漏）
func TestCloseAll(t *testing.T) {
	m, sessions := setupTestManager(t)

	m.OnTunnelUpdate(map[string]P2PConfig{
		"a": p2pCfg("closeaaa01"),
		"b": p2pCfg("closebbb01"),
	})
	waitFor(t, func() bool { return sessions.len() >= 2 }, 2*time.Second, "handlers not started")

	m.Close()
	if _, err := m.Status("a"); err != ErrNotFound {
		t.Fatalf("Status after Close = %v, want ErrNotFound", err)
	}
	m.Close() // 幂等
}
