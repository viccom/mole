package stun

import (
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/stun/v3"
)

// startTestServer 起一个回环随机端口实例，返回服务与客户端 UDP 连接
func startTestServer(t *testing.T) (*Server, net.Conn) {
	t.Helper()
	s, err := NewServer("127.0.0.1:0")
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	go func() {
		if err := s.ListenAndServe(); err != nil {
			t.Errorf("ListenAndServe: %v", err)
		}
	}()

	conn, err := net.Dial("udp", s.LocalAddr().String())
	if err != nil {
		t.Fatalf("dial %s: %v", s.LocalAddr(), err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	return s, conn
}

// Binding Request 必须得到 Binding Success 应答，且 XOR-MAPPED-ADDRESS 回显
// 客户端观测地址——这是 P2P 客户端 NAT 探测兜底链路的核心契约
// （公共 STUN 不可达时的 server :3478 兜底）。连续两次验证服务循环持续应答。
func TestBindingRoundtrip(t *testing.T) {
	_, conn := startTestServer(t)

	for i := 0; i < 2; i++ {
		req := stun.MustBuild(stun.NewTransactionIDSetter(stun.NewTransactionID()), stun.BindingRequest)
		if _, err := conn.Write(req.Raw); err != nil {
			t.Fatalf("round %d: write request: %v", i, err)
		}
		raw := make([]byte, 1500)
		n, err := conn.Read(raw)
		if err != nil {
			t.Fatalf("round %d: read response: %v", i, err)
		}
		res := &stun.Message{Raw: raw[:n]}
		if err := res.Decode(); err != nil {
			t.Fatalf("round %d: decode response: %v", i, err)
		}
		if res.Type != stun.BindingSuccess {
			t.Fatalf("round %d: response type = %v, want BindingSuccess", i, res.Type)
		}
		var xor stun.XORMappedAddress
		if err := xor.GetFrom(res); err != nil {
			t.Fatalf("round %d: XORMappedAddress missing: %v", i, err)
		}
		local := conn.LocalAddr().(*net.UDPAddr)
		if xor.Port != local.Port {
			t.Errorf("round %d: xor.Port = %d, want %d", i, xor.Port, local.Port)
		}
		if !xor.IP.Equal(local.IP) {
			t.Errorf("round %d: xor.IP = %v, want %v", i, xor.IP, local.IP)
		}
	}
}

// 端口上出现的非 STUN 报文（探测/噪声）必须被忽略，且不影响后续 Binding 应答
func TestNonStunPacketIgnored(t *testing.T) {
	_, conn := startTestServer(t)

	if _, err := conn.Write([]byte("not a stun packet")); err != nil {
		t.Fatalf("write garbage: %v", err)
	}
	req := stun.MustBuild(stun.NewTransactionIDSetter(stun.NewTransactionID()), stun.BindingRequest)
	if _, err := conn.Write(req.Raw); err != nil {
		t.Fatalf("write request: %v", err)
	}

	raw := make([]byte, 1500)
	n, err := conn.Read(raw)
	if err != nil {
		t.Fatalf("read response after garbage: %v", err)
	}
	res := &stun.Message{Raw: raw[:n]}
	if err := res.Decode(); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if res.Type != stun.BindingSuccess {
		t.Fatalf("response type = %v, want BindingSuccess", res.Type)
	}
}

// flakyPacketConn 模拟瞬时 UDP 错误：前 N 次 ReadFrom/WriteTo 立即报错，其余委托真实 conn
type flakyPacketConn struct {
	net.PacketConn
	failReads  atomic.Int32
	failWrites atomic.Int32
}

func (c *flakyPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	if c.failReads.Add(-1) >= 0 {
		return 0, nil, errors.New("simulated transient read error")
	}
	return c.PacketConn.ReadFrom(p)
}

func (c *flakyPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	if c.failWrites.Add(-1) >= 0 {
		return 0, errors.New("simulated transient write error")
	}
	return c.PacketConn.WriteTo(p, addr)
}

// STUN 是可选兜底组件：瞬时读/写错误（接口抖动、ENOBUFS、ICMP 偶发反馈）
// 绝不允许终止服务循环——main.go 对 ListenAndServe 返回错误的响应是 cancel
// 根 ctx，等于让整个 server 进程陪葬。本测试证明错误后循环存活并继续应答。
func TestListenAndServeSurvivesTransientErrors(t *testing.T) {
	s, err := NewServer("127.0.0.1:0")
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	flaky := &flakyPacketConn{PacketConn: s.conn}
	flaky.failReads.Store(2)
	flaky.failWrites.Store(1)
	s.conn = flaky

	servedErr := make(chan error, 1)
	go func() { servedErr <- s.ListenAndServe() }()

	conn, err := net.Dial("udp", s.LocalAddr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	// 穿过 2 次读错误 + 1 次写错误后，必须仍能完成至少 2 次完整应答
	deadline := time.Now().Add(5 * time.Second)
	successes := 0
	for successes < 2 && time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		req := stun.MustBuild(stun.NewTransactionIDSetter(stun.NewTransactionID()), stun.BindingRequest)
		if _, err := conn.Write(req.Raw); err != nil {
			t.Fatalf("write request: %v", err)
		}
		raw := make([]byte, 1500)
		n, err := conn.Read(raw)
		if err != nil {
			continue // 写失败丢掉的那次响应：超时重发
		}
		res := &stun.Message{Raw: raw[:n]}
		if err := res.Decode(); err == nil && res.Type == stun.BindingSuccess {
			successes++
		}
	}
	if successes < 2 {
		t.Fatalf("only %d successful roundtrips after transient errors — service loop died?", successes)
	}

	// 服务循环必须仍在运行（未因瞬时错误退出）
	select {
	case err := <-servedErr:
		t.Fatalf("ListenAndServe exited early: %v", err)
	default:
	}

	// Close 后循环必须正常返回（nil）
	_ = s.Close()
	select {
	case err := <-servedErr:
		if err != nil {
			t.Fatalf("ListenAndServe after Close = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ListenAndServe did not return after Close")
	}
}

// 复审 R4：连续错误退避指数增长、5s 封顶（防持久性故障下刷日志与空转）
func TestStunErrBackoff(t *testing.T) {
	want := []time.Duration{
		100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond,
		800 * time.Millisecond, 1600 * time.Millisecond, 3200 * time.Millisecond,
		5 * time.Second, 5 * time.Second, 5 * time.Second,
	}
	for i, w := range want {
		if got := stunErrBackoff(i + 1); got != w {
			t.Fatalf("stunErrBackoff(%d) = %v, want %v", i+1, got, w)
		}
	}
}
