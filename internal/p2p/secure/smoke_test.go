//go:build p2p

package secure

import (
	"io"
	"net"
	"testing"
)

// Phase 0 冒烟测试：验证 fork 的 secure 包在 ss+PSK 层下双端 DoNegotiation 能成功返回，
// 且 PSK 派生的 KeyingMaterial 两端一致（DerivePSK 是确定性的）。
// 数据往返留到 Phase 1 secureUpgrade 的 DTLS+真实 UDP 测试覆盖（ss 的 stream-IV 协议在
// net.Pipe 同步语义下时序敏感，此处只验证握手与密钥派生）。
func TestDoNegotiation_SS_PSK_Handshake(t *testing.T) {
	a, b := net.Pipe()

	cfg := func(isClient bool) *NegotiationConfig {
		return &NegotiationConfig{
			IsClient:    isClient,
			SecureLayer: "ss",
			KeyType:     "PSK",
			Key:         "test-room-psk",
		}
	}

	type res struct {
		n   *NegotiatedConn
		err error
	}
	srvCh, cliCh := make(chan res, 1), make(chan res, 1)

	go func() { n, e := DoNegotiation(cfg(false), a, io.Discard); srvCh <- res{n, e} }()
	go func() { n, e := DoNegotiation(cfg(true), b, io.Discard); cliCh <- res{n, e} }()

	srv := <-srvCh
	cli := <-cliCh
	if srv.err != nil {
		t.Fatalf("server DoNegotiation: %v", srv.err)
	}
	if cli.err != nil {
		t.Fatalf("client DoNegotiation: %v", cli.err)
	}
	defer srv.n.Close()
	defer cli.n.Close()

	if srv.n.TopLayer == nil || cli.n.TopLayer == nil {
		t.Fatal("TopLayer nil after negotiate")
	}
	if srv.n.KeyingMaterial != cli.n.KeyingMaterial {
		t.Fatal("KeyingMaterial mismatch: PSK derive must be deterministic and equal on both ends")
	}
	// ss over TCP 不应误判为 UDP，也不应套 KCP
	if srv.n.IsUDP || cli.n.IsUDP {
		t.Fatal("net.Pipe must not be detected as UDP")
	}
	if srv.n.WithKCP || cli.n.WithKCP {
		t.Fatal("ss over TCP must not enable KCP")
	}
}
