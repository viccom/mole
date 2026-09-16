//go:build p2p

package p2p

import (
	"context"
	"errors"
	"strings"
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

func (m *mockSession) CloseTunnel(id uint32) error { return nil }

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
	return P2PConfig{Room: "roomOK123456", Modes: []string{"lan"},
		Mappings: []Mapping{{Protocol: "tcp", LocalPort: 18080, TargetHost: "127.0.0.1", TargetPort: 8080}}}
}

// 发起端：session 建立后必须 CreateTunnel（本地监听 + 由对端 dial target）
func TestHandlerRunWithTargetCreatesTunnel(t *testing.T) {
	sessions := withFailingConnect(t, 0, nil)
	h := NewHandler(initiatorCfg(), "127.0.0.1", nil)

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

// 多组映射：restoreTunnel 必须逐条 CreateTunnel（每条 mapping 一个独立
// tunnel，fork 原生多隧道），参数逐项对应
func TestHandlerRunMultiMappingCreatesEachTunnel(t *testing.T) {
	sessions := withFailingConnect(t, 0, nil)
	cfg := P2PConfig{Room: "roomOK123456", Modes: []string{"lan"}, Mappings: []Mapping{
		{Protocol: "tcp", LocalPort: 18080, TargetHost: "127.0.0.1", TargetPort: 80},
		{Protocol: "udp", LocalPort: 18081, TargetHost: "10.0.0.2", TargetPort: 53},
	}}
	h := NewHandler(cfg, "127.0.0.1", nil)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = h.Run(ctx) }()
	t.Cleanup(cancel)

	waitFor(t, func() bool { return sessions.len() > 0 && sessions.at(0).createCount() == 2 },
		2*time.Second, "expected 2 CreateTunnel calls for 2 mappings")
	calls := sessions.at(0).createCallsForAssert()
	want := []tunnel.Params{
		{Protocol: "tcp", LocalPort: 18080, TargetHost: "127.0.0.1", TargetPort: 80},
		{Protocol: "udp", LocalPort: 18081, TargetHost: "10.0.0.2", TargetPort: 53},
	}
	for i, w := range want {
		if calls[i] != w {
			t.Fatalf("CreateTunnel[%d] = %+v, want %+v", i, calls[i], w)
		}
	}
}

// 部分映射建失败：视同未连通走退避重连，而不是带着残缺监听继续跑
//（F5 语义延伸：本端监听不完整不能算连通）
func TestHandlerPartialMappingFailureTriggersBackoff(t *testing.T) {
	// 压缩重试间隔与退避（注册序即逆清理序：延迟恢复最后执行，此刻 Run 已退出）
	origDelay := tunnelRetryDelay
	tunnelRetryDelay = time.Millisecond
	t.Cleanup(func() { tunnelRetryDelay = origDelay })
	origBackoff := backoffFn
	backoffFn = func(int) time.Duration { return time.Millisecond }
	t.Cleanup(func() { backoffFn = origBackoff })

	attempts := &intSlice{}
	origConnect := connectFn
	connectFn = func(ctx context.Context, modeName string, cfg P2PConfig, host string, creds SignalCredentials) (session.Session, error) {
		attempts.add(0)
		return &partialCreateSession{mockSession: newMockSession()}, nil
	}
	t.Cleanup(func() { connectFn = origConnect })

	cfg := P2PConfig{Room: "roomOK123456", Modes: []string{"lan"}, Mappings: []Mapping{
		{Protocol: "tcp", LocalPort: 18080, TargetHost: "127.0.0.1", TargetPort: 80},
		{Protocol: "tcp", LocalPort: 18081, TargetHost: "127.0.0.1", TargetPort: 81},
	}}
	h := NewHandler(cfg, "127.0.0.1", nil)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() { _ = h.Run(ctx); close(runDone) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-runDone:
		case <-time.After(2 * time.Second):
		}
	})

	// 第一条成功、第二条持续失败 → 整轮视同未连通 → 退避后重连（第二次 connect）
	waitFor(t, func() bool { return len(attempts.snapshot()) >= 2 },
		3*time.Second, "partial mapping failure must enter backoff and reconnect")
	// 轮询而非立即读：重连瞬间的 setSession 会先清 lastErr，新一轮失败重写前有空窗
	waitFor(t, func() bool { return strings.Contains(h.Status().Error, "FAILED") },
		3*time.Second, "failure summary must surface in Status.Error")
}

// partialCreateSession 第 2 条起 CreateTunnel 永远失败（模拟第二个端口被占用）
type partialCreateSession struct{ *mockSession }

func (m *partialCreateSession) CreateTunnel(p tunnel.Params) (session.TunnelInfo, error) {
	if m.createCount() >= 1 {
		return session.TunnelInfo{}, errors.New("bind: address already in use")
	}
	return m.mockSession.CreateTunnel(p)
}

// 纯会话端：只建 session 供对端 OPEN，不得 CreateTunnel
func TestHandlerPureSessionNoTunnel(t *testing.T) {
	sessions := withFailingConnect(t, 0, nil)
	cfg := P2PConfig{Room: "roomOK123456"} // 无映射 = 纯会话端
	h := NewHandler(cfg, "127.0.0.1", nil)

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
	h := NewHandler(cfg, "127.0.0.1", nil)

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
	h := NewHandler(initiatorCfg(), "127.0.0.1", nil)

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
	h := NewHandler(initiatorCfg(), "127.0.0.1", nil)
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

// 凭据注入：credsFn 产物传给 connectFn；拉取失败时以匿名继续（不中断连接循环），
// 下一轮重试重拉
func TestHandlerCredsInjection(t *testing.T) {
	origConnect, origBackoff := connectFn, backoffFn
	t.Cleanup(func() { connectFn, backoffFn = origConnect, origBackoff })
	backoffFn = func(int) time.Duration { return time.Millisecond }

	var mu sync.Mutex
	var gotCreds []SignalCredentials
	credCalls := 0
	var sess *mockSession

	connectFn = func(ctx context.Context, modeName string, cfg P2PConfig, host string, creds SignalCredentials) (session.Session, error) {
		mu.Lock()
		gotCreds = append(gotCreds, creds)
		n := len(gotCreds)
		mu.Unlock()
		if n == 1 {
			return nil, context.DeadlineExceeded
		}
		s := newMockSession()
		mu.Lock()
		sess = s
		mu.Unlock()
		return s, nil
	}
	// 确定性时序：第 1 次拉取失败（匿名回落），之后成功
	credsFn := func(ctx context.Context) (SignalCredentials, error) {
		mu.Lock()
		defer mu.Unlock()
		credCalls++
		if credCalls == 1 {
			return SignalCredentials{}, errors.New("control plane down")
		}
		return SignalCredentials{Username: "p2p-signal:x", Password: "pw"}, nil
	}

	h := NewHandler(initiatorCfg(), "127.0.0.1", credsFn)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = h.Run(ctx) }()
	t.Cleanup(cancel)

	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return sess != nil },
		2*time.Second, "never connected after creds recovery")

	mu.Lock()
	defer mu.Unlock()
	if len(gotCreds) < 2 {
		t.Fatalf("expected at least 2 connect attempts, got %d", len(gotCreds))
	}
	if gotCreds[0] != (SignalCredentials{}) {
		t.Fatalf("first attempt (credsFn error) must fall back to anonymous, got %+v", gotCreds[0])
	}
	if gotCreds[1] != (SignalCredentials{Username: "p2p-signal:x", Password: "pw"}) {
		t.Fatalf("creds from credsFn must reach connectFn, got %+v", gotCreds[1])
	}
}

// ===== 复审 F4/F5/F15 =====

// 单次尝试超时（复审 F4）：connectFn 被信令/broker 永久阻塞时，
// 看门狗必须打断并进入退避，而不是把 Handler 永久楔死
func TestModeAttemptTimeout(t *testing.T) {
	sessions := &sessionRecorder{}
	origConnect := connectFn
	connectFn = func(ctx context.Context, modeName string, cfg P2PConfig, host string, creds SignalCredentials) (session.Session, error) {
		<-ctx.Done() // 模拟 broker 拒绝后 ConnectRetry 永不返回
		return nil, ctx.Err()
	}
	t.Cleanup(func() { connectFn = origConnect })

	cfg := initiatorCfg()
	h := NewHandler(cfg, "127.0.0.1", nil)
	h.modeAttemptTimeout = 30 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = h.Run(ctx); close(done) }()
	// 等待 Run goroutine 退出后再返回：泄漏的 goroutine 会继续读包级
	// backoffFn/connectFn，与后续测试的注入写入构成数据竞争
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rt := h.Status()
		if strings.Contains(rt.Error, "attempt timeout") {
			if sessions.len() != 0 {
				t.Fatalf("no session should exist after timeout, got %d", sessions.len())
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("attempt timeout never surfaced in Status")
}

// 建隧道持续失败必须计入退避（复审 F5）：否则 3s 快速重连反复完整重打洞
// （conntrack 洪泛源）。此测试在修复前会失败（connected=true 重置退避）
func TestTunnelCreationFailureTriggersBackoff(t *testing.T) {
	spy := &intSlice{}
	origConnect := connectFn
	connectFn = func(ctx context.Context, modeName string, cfg P2PConfig, host string, creds SignalCredentials) (session.Session, error) {
		spy.add(0)
		return &failCreateSession{mockSession: newMockSession()}, nil
	}
	t.Cleanup(func() { connectFn = origConnect })

	h := NewHandler(initiatorCfg(), "127.0.0.1", nil)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() { _ = h.Run(ctx); close(runDone) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-runDone:
		case <-time.After(2 * time.Second):
		}
	})

	deadline := time.Now().Add(2 * time.Second)
	sawBackoff := false
	for time.Now().Before(deadline) && !sawBackoff {
		if len(spy.snapshot()) > 0 {
			sawBackoff = true
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !sawBackoff {
		t.Fatal("CreateTunnel persistent failure must enter backoff (connected must be false)")
	}
}

// failCreateSession CreateTunnel 永远失败的会话（模拟对端不应答 OPEN）
type failCreateSession struct{ *mockSession }

func (m *failCreateSession) CreateTunnel(p tunnel.Params) (session.TunnelInfo, error) {
	return session.TunnelInfo{}, errors.New("bind: address already in use")
}

// 字节计数跨重连累计（复审 F15）：重连归零的会话计数不得让总量塌缩为 0
func TestStatusCountersSurviveReconnect(t *testing.T) {
	sessions := withFailingConnect(t, 0, nil)
	h := NewHandler(initiatorCfg(), "127.0.0.1", nil)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = h.Run(ctx) }()
	t.Cleanup(cancel)

	waitFor(t, func() bool { return sessions.len() > 0 }, 2*time.Second, "session not created")
	s1 := sessions.at(0)
	s1.mu.Lock()
	s1.tunnels = []session.TunnelInfo{{ID: 1, BytesIn: 100, BytesOut: 50}}
	s1.mu.Unlock()
	rt1 := h.Status()
	if rt1.BytesIn != 100 || rt1.BytesOut != 50 {
		t.Fatalf("first session counters = %d/%d", rt1.BytesIn, rt1.BytesOut)
	}

	// 模拟重连：Handler 换了新会话（计数从 0 开始）
	s2 := newMockSession()
	s2.tunnels = []session.TunnelInfo{{ID: 1, BytesIn: 30, BytesOut: 70}}
	h.mu.Lock()
	h.sess = s2
	h.mu.Unlock()
	rt2 := h.Status()
	if rt2.BytesIn != 130 || rt2.BytesOut != 120 {
		t.Fatalf("counters collapsed on reconnect: %d/%d, want 130/120", rt2.BytesIn, rt2.BytesOut)
	}

	// 再换一个空会话：累计值保持，不塌缩为 0
	s3 := newMockSession()
	h.mu.Lock()
	h.sess = s3
	h.mu.Unlock()
	rt3 := h.Status()
	if rt3.BytesIn != 130 || rt3.BytesOut != 120 {
		t.Fatalf("counters collapsed on empty session: %d/%d", rt3.BytesIn, rt3.BytesOut)
	}
}
