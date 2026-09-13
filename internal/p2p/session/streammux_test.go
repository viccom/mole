//go:build p2p

package session

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

// 验证 yamuxSession 适配器：net.Pipe 上建 yamux Client/Server，OpenStream/AcceptStream 往返数据。
func TestYamuxStreamMux_RoundTrip(t *testing.T) {
	a, b := net.Pipe()

	var srv, cli StreamMux
	srvErr, cliErr := make(chan error, 1), make(chan error, 1)
	go func() {
		s, e := yamux.Server(a, nil)
		if e == nil {
			srv = NewYamuxStreamMux(s)
		}
		srvErr <- e
	}()
	go func() {
		s, e := yamux.Client(b, nil)
		if e == nil {
			cli = NewYamuxStreamMux(s)
		}
		cliErr <- e
	}()
	if e := <-srvErr; e != nil {
		t.Fatalf("yamux server: %v", e)
	}
	if e := <-cliErr; e != nil {
		t.Fatalf("yamux client: %v", e)
	}
	defer srv.Close()
	defer cli.Close()

	// server 端先就位 accept，client 再 open+write
	type acceptRes struct {
		c net.Conn
		e error
	}
	ac := make(chan acceptRes, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c, e := srv.AcceptStream(ctx)
		ac <- acceptRes{c, e}
	}()
	time.Sleep(80 * time.Millisecond) // 让 accept goroutine 抢先

	out, err := cli.OpenStream()
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	defer out.Close()
	if _, err := out.Write([]byte("hello-mux")); err != nil {
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
	if string(buf[:n]) != "hello-mux" {
		t.Fatalf("got %q want hello-mux", buf[:n])
	}
}
