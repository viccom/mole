//go:build p2p

package engine

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"moleAgent_client/internal/p2p/session"
)

// 验证 secureUpgrade TCP 路径：net.Pipe → DoNegotiation(tls13+PSK) → yamux → stream 往返。
// UDP（DTLS+KCP）路径需真实 UDP socket，留到 Phase 3 真机验证；secureUpgrade 的主体逻辑
// （NegotiationConfig 组装 + 证书派生 + DoNegotiation + yamux）在此被覆盖。
func TestSecureUpgrade_TCP_RoundTrip(t *testing.T) {
	a, b := net.Pipe()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 1)
	}

	type res struct {
		m session.StreamMux
		e error
	}
	sc, cc := make(chan res, 1), make(chan res, 1)
	go func() {
		m, e := secureUpgrade(context.Background(), a, &key, false, false, io.Discard)
		sc <- res{m, e}
	}()
	go func() {
		m, e := secureUpgrade(context.Background(), b, &key, true, false, io.Discard)
		cc <- res{m, e}
	}()

	sr := <-sc
	cr := <-cc
	if sr.e != nil {
		t.Fatalf("server secureUpgrade: %v", sr.e)
	}
	if cr.e != nil {
		t.Fatalf("client secureUpgrade: %v", cr.e)
	}
	defer sr.m.Close()
	defer cr.m.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	type ar struct {
		c net.Conn
		e error
	}
	ac := make(chan ar, 1)
	go func() {
		c, e := sr.m.AcceptStream(ctx)
		ac <- ar{c, e}
	}()
	time.Sleep(120 * time.Millisecond) // 让 secureUpgrade 两端 yamux 就绪 + accept 抢先

	out, err := cr.m.OpenStream()
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	defer out.Close()
	if _, err := out.Write([]byte("over-secure")); err != nil {
		t.Fatalf("write: %v", err)
	}

	r := <-ac
	if r.e != nil {
		t.Fatalf("AcceptStream: %v", r.e)
	}
	defer r.c.Close()
	buf := make([]byte, 32)
	n, err := r.c.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf[:n]) != "over-secure" {
		t.Fatalf("got %q want over-secure", buf[:n])
	}
}

// unwrapUDP 对裸 *net.UDPConn 应直接返回自身（gonc LAN/直连路径）。
// *netx.ConnFromPacketConn 分支在真实打洞路径（Phase 3）覆盖。
func TestUnwrapUDP_BareUDPConn(t *testing.T) {
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer c.Close()
	if got := unwrapUDP(c); got == nil {
		t.Fatal("unwrapUDP returned nil for *net.UDPConn")
	}
	if got := unwrapUDP(c); got != c {
		t.Fatal("unwrapUDP should return the same *net.UDPConn")
	}
}
