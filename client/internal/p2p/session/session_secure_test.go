//go:build p2p

package session

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

// 验证 secureSession 端到端：net.Pipe + yamux 双端 → NewSession（并发配对 mainStream）
// → client SendText 经 mainStream 文本协议（MSG: 前缀）到达 server OnMessage。
// 覆盖 mainStream open/accept 配对、reader goroutine 解析、msgPrefix 协议。
func TestSecureSession_SendText_RoundTrip(t *testing.T) {
	a, b := net.Pipe()

	var srvMux, cliMux StreamMux
	srvErr, cliErr := make(chan error, 1), make(chan error, 1)
	go func() {
		s, e := yamux.Server(a, nil)
		if e == nil {
			srvMux = NewYamuxStreamMux(s)
		}
		srvErr <- e
	}()
	go func() {
		s, e := yamux.Client(b, nil)
		if e == nil {
			cliMux = NewYamuxStreamMux(s)
		}
		cliErr <- e
	}()
	if e := <-srvErr; e != nil {
		t.Fatalf("yamux server: %v", e)
	}
	if e := <-cliErr; e != nil {
		t.Fatalf("yamux client: %v", e)
	}

	got := make(chan string, 1)

	// NewSession 并发：server Accept mainStream + client Open mainStream 必须并发，
	// 否则先调者阻塞等对端。
	type ns struct {
		s Session
		e error
	}
	srvCh, cliCh := make(chan ns, 1), make(chan ns, 1)
	go func() {
		s, e := NewSession(context.Background(), &Outcome{Mux: srvMux, IsClient: false},
			WithOnMessage(func(s string) { got <- s }))
		srvCh <- ns{s, e}
	}()
	go func() {
		s, e := NewSession(context.Background(), &Outcome{Mux: cliMux, IsClient: true})
		cliCh <- ns{s, e}
	}()
	srv := <-srvCh
	cli := <-cliCh
	if srv.e != nil {
		t.Fatalf("server NewSession: %v", srv.e)
	}
	if cli.e != nil {
		t.Fatalf("client NewSession: %v", cli.e)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	go srv.s.Run(ctx)
	go cli.s.Run(ctx)

	time.Sleep(150 * time.Millisecond) // 等 reader goroutine 就绪
	if err := cli.s.SendText("hello-secure"); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	select {
	case msg := <-got:
		if msg != "hello-secure" {
			t.Fatalf("got %q want hello-secure", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for message")
	}
	srv.s.Close()
	cli.s.Close()
}
