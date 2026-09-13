package stun

import (
	"net"
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
