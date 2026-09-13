//go:build p2p

package p2p

import (
	"context"
	"sync"
	"testing"
	"time"

	"moleAgent_client/internal/p2p/session"
	"moleAgent_client/internal/p2p/tunnel"
)

// mockSession 实现 session.Session（嵌入 nil 接口补齐未用到的方法）。
// Run 阻塞直到 ctx 取消或 release 关闭——模拟真实长连接。
type mockSession struct {
	session.Session

	mu          sync.Mutex
	createCalls []tunnel.Params
	tunnels     []session.TunnelInfo
	closed      bool

	runEntered chan struct{}
	release    chan struct{}
	releaseOne sync.Once
}

func newMockSession() *mockSession {
	return &mockSession{runEntered: make(chan struct{}), release: make(chan struct{})}
}

func (m *mockSession) Run(ctx context.Context) error {
	select {
	case <-m.runEntered:
	default:
		close(m.runEntered)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.release:
		return nil
	}
}

func (m *mockSession) CreateTunnel(p tunnel.Params) (session.TunnelInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createCalls = append(m.createCalls, p)
	return session.TunnelInfo{ID: uint32(len(m.createCalls)), Protocol: p.Protocol,
		LocalPort: p.LocalPort, TargetHost: p.TargetHost, TargetPort: p.TargetPort}, nil
}

func (m *mockSession) ListTunnels() []session.TunnelInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]session.TunnelInfo, len(m.tunnels))
	copy(out, m.tunnels)
	return out
}

func (m *mockSession) Close() error {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	m.releaseOne.Do(func() { close(m.release) })
	return nil
}

func (m *mockSession) wasClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

func (m *mockSession) createCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.createCalls)
}

func (m *mockSession) createCallsForAssert() []tunnel.Params {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]tunnel.Params, len(m.createCalls))
	copy(out, m.createCalls)
	return out
}

// sessionRecorder 并发安全地记录 connectFn 产出的 session（race detector 下
// 测试主 goroutine 与 Handler goroutine 并发读写）
type sessionRecorder struct {
	mu sync.Mutex
	s  []*mockSession
}

func (r *sessionRecorder) add(s *mockSession) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.s = append(r.s, s)
}

func (r *sessionRecorder) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.s)
}

func (r *sessionRecorder) at(i int) *mockSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.s[i]
}

// intSlice 并发安全的退避参数记录器
type intSlice struct {
	mu sync.Mutex
	v  []int
}

func (s *intSlice) add(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.v = append(s.v, n)
}

func (s *intSlice) snapshot() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int(nil), s.v...)
}

// withFailingConnect 让 connectFn 失败 n 次后成功。
// 退避时长压缩为 1ms（序列语义由 TestReconnectBackoffSequence 覆盖），
// 这里只验证失败计数与重试顺序。
func withFailingConnect(t *testing.T, failTimes int, backoffSpy *intSlice) *sessionRecorder {
	t.Helper()
	origBackoff := backoffFn
	backoffFn = func(failures int) time.Duration {
		if backoffSpy != nil {
			backoffSpy.add(failures)
		}
		return time.Millisecond
	}
	t.Cleanup(func() { backoffFn = origBackoff })

	rec := &sessionRecorder{}
	attempts := 0
	var mu sync.Mutex
	origConnect := connectFn
	connectFn = func(ctx context.Context, modeName string, cfg P2PConfig, host string, creds SignalCredentials) (session.Session, error) {
		mu.Lock()
		attempts++
		n := attempts
		mu.Unlock()
		if n <= failTimes {
			return nil, context.DeadlineExceeded // 模拟打洞失败
		}
		s := newMockSession()
		rec.add(s)
		return s, nil
	}
	t.Cleanup(func() { connectFn = origConnect })
	return rec
}

func initiatorCfg() P2PConfig {
	// 显式单 mode：每个连接循环轮次恰好一次尝试，退避计数可精确断言
	return P2PConfig{Room: "roomOK123456", Modes: []string{"lan"}, Protocol: "tcp",
		LocalPort: 18080, TargetHost: "127.0.0.1", TargetPort: 8080}
}

// 发起端：session 建立后必须 CreateTunnel（本地监听 + 由对端 dial target）
func TestHandlerRunWithTargetCreatesTunnel(t *testing.T) {
	sessions := withFailingConnect(t, 0, nil)
	h := NewHandler(initiatorCfg(), "127.0.0.1", SignalCredentials{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = h.Run(ctx); close(done) }()
	t.Cleanup(cancel)

	waitFor(t, func() bool { return sessions.len() > 0 && sessions.at(0).createCount() == 1 },
		2*time.Second, "CreateTunnel not called")
	calls := sessions.at(0).createCallsForAssert()
	if calls[0] != (tunnel.Params{Protocol: "tcp", LocalPort: 18080, TargetHost: "127.0.0.1", TargetPort: 8080}) {
		t.Fatalf("CreateTunnel params = %+v", calls[0])
	}
}

// 纯会话端：只建 session 供对端 OPEN，不得 CreateTunnel
func TestHandlerPureSessionNoTunnel(t *testing.T) {
	sessions := withFailingConnect(t, 0, nil)
	cfg := P2PConfig{Room: "roomOK123456", Protocol: "tcp"} // 无 target
	h := NewHandler(cfg, "127.0.0.1", SignalCredentials{})

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = h.Run(ctx) }()
	t.Cleanup(cancel)

	waitFor(t, func() bool { return sessions.len() > 0 }, 2*time.Second, "session not created")
	time.Sleep(50 * time.Millisecond)
	if n := sessions.at(0).createCount(); n != 0 {
		t.Fatalf("pure session side must not CreateTunnel, got %d calls", n)
	}
}

// 打洞失败按 5/10/20/40/60s 退避（防 conntrack 爆表，勿改回固定快重连）
func TestReconnectBackoffSequence(t *testing.T) {
	want := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second, 60 * time.Second, 60 * time.Second}
	for i, w := range want {
		if got := reconnectBackoff(i + 1); got != w {
			t.Fatalf("reconnectBackoff(%d) = %v, want %v", i+1, got, w)
		}
	}
}

// 断开后按退避重连：失败次数递增、重置时机为「连上过 session」
func TestHandlerBackoffRetry(t *testing.T) {
	spy := &intSlice{}
	sessions := withFailingConnect(t, 2, spy)
	cfg := initiatorCfg()
	h := NewHandler(cfg, "127.0.0.1", SignalCredentials{})

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = h.Run(ctx) }()
	t.Cleanup(cancel)

	waitFor(t, func() bool { return sessions.len() > 0 }, 2*time.Second, "never connected after backoff retries")
	got := spy.snapshot()
	if len(got) != 2 {
		t.Fatalf("backoff invocations = %v, want 2 failures before success", got)
	}
	if got[0] != 1 || got[1] != 2 {
		t.Fatalf("backoff failure sequence = %v, want [1 2]", got)
	}
}

// Status 聚合 ListTunnels 的字节统计（TunnelInfo 自带 BytesIn/Out）
func TestHandlerStatusAggregatesBytes(t *testing.T) {
	sessions := withFailingConnect(t, 0, nil)
	h := NewHandler(initiatorCfg(), "127.0.0.1", SignalCredentials{})

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = h.Run(ctx) }()
	t.Cleanup(cancel)

	waitFor(t, func() bool { return sessions.len() > 0 }, 2*time.Second, "session not created")
	s := sessions.at(0)
	s.mu.Lock()
	s.tunnels = []session.TunnelInfo{{ID: 1, BytesIn: 100, BytesOut: 50}, {ID: 2, BytesIn: 30, BytesOut: 70}}
	s.mu.Unlock()

	rt := h.Status()
	if !rt.Connected {
		t.Fatal("Status.Connected = false with live session")
	}
	if rt.BytesIn != 130 || rt.BytesOut != 120 {
		t.Fatalf("bytes = %d/%d, want 130/120", rt.BytesIn, rt.BytesOut)
	}
}

// Close 幂等取消：Run 返回、session 关闭
func TestHandlerClose(t *testing.T) {
	sessions := withFailingConnect(t, 0, nil)
	h := NewHandler(initiatorCfg(), "127.0.0.1", SignalCredentials{})
	h.Start(context.Background())

	waitFor(t, func() bool { return sessions.len() > 0 }, 2*time.Second, "session not created")

	h.Close()
	h.Close() // 幂等

	if !sessions.at(0).wasClosed() {
		t.Fatal("Close must close the session")
	}
}

// waitFor 轮询等待条件成立（异步生命周期断言的统一辅助）
func waitFor(t *testing.T, cond func() bool, timeout time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(msg)
}
