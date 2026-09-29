package tunnel

import (
	"crypto/sha256"
	"testing"
	"time"

	kcp "github.com/xtaci/kcp-go/v5"
)

func TestKCPTransport_Name(t *testing.T) {
	got := (&KCPTransport{}).Name()
	if got != "kcp" {
		t.Errorf("expected kcp, got %s", got)
	}
}

func TestKCPTransport_ListenAndDial(t *testing.T) {
	ln, err := (&KCPTransport{}).Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()

	go func() {
		conn, err := kcp.DialWithOptions(addr, nil, 0, 0)
		if err != nil {
			t.Logf("dial: %v", err)
			return
		}
		defer conn.Close()
		conn.Write([]byte("hello kcp"))
		time.Sleep(2 * time.Second)
	}()

	ln.(*kcpListener).SetDeadline(time.Now().Add(5 * time.Second))
	srvConn, err := ln.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	defer srvConn.Close()

	buf := make([]byte, 64)
	srvConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, err := srvConn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf[:n]) != "hello kcp" {
		t.Errorf("expected 'hello kcp', got %q", buf[:n])
	}
}

func TestKCPTransport_WithEncryption(t *testing.T) {
	key := "test-secret-key-1234567890"
	cfg := KCPConfig{Key: key}
	ln, err := NewKCPTransport(cfg).Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()
	h := sha256.Sum256([]byte(key))
	block, _ := kcp.NewAESBlockCrypt(h[:])

	go func() {
		conn, err := kcp.DialWithOptions(addr, block, 0, 0)
		if err != nil {
			t.Logf("dial: %v", err)
			return
		}
		defer conn.Close()
		conn.Write([]byte("encrypted hello"))
		time.Sleep(2 * time.Second)
	}()

	ln.(*kcpListener).SetDeadline(time.Now().Add(5 * time.Second))
	srvConn, err := ln.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	defer srvConn.Close()

	buf := make([]byte, 64)
	srvConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, err := srvConn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf[:n]) != "encrypted hello" {
		t.Errorf("expected 'encrypted hello', got %q", buf[:n])
	}
}

func TestKCPTransport_WithFEC(t *testing.T) {
	cfg := KCPConfig{DataShards: 10, ParityShards: 3}
	ln, err := NewKCPTransport(cfg).Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()

	go func() {
		conn, err := kcp.DialWithOptions(addr, nil, cfg.DataShards, cfg.ParityShards)
		if err != nil {
			t.Logf("dial: %v", err)
			return
		}
		defer conn.Close()
		conn.Write([]byte("fec protected"))
		time.Sleep(2 * time.Second)
	}()

	ln.(*kcpListener).SetDeadline(time.Now().Add(5 * time.Second))
	srvConn, err := ln.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	defer srvConn.Close()

	buf := make([]byte, 64)
	srvConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, err := srvConn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf[:n]) != "fec protected" {
		t.Errorf("expected 'fec protected', got %q", buf[:n])
	}
}

func TestKCPTransport_NegativeShardsClamped(t *testing.T) {
	cfg := KCPConfig{DataShards: -5, ParityShards: -3}
	tt := NewKCPTransport(cfg)
	if tt.Config.DataShards != 0 {
		t.Errorf("expected DataShards=0, got %d", tt.Config.DataShards)
	}
	if tt.Config.ParityShards != 0 {
		t.Errorf("expected ParityShards=0, got %d", tt.Config.ParityShards)
	}
}

func TestKCPTransport_AcceptAfterClose(t *testing.T) {
	ln, err := (&KCPTransport{}).Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ln.Close()

	_, err = ln.Accept()
	if err == nil {
		t.Error("expected error after close")
	}
}

func TestKCPTransport_Addresses(t *testing.T) {
	ln, err := (&KCPTransport{}).Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr()
	if addr == nil {
		t.Fatal("addr should not be nil")
	}
	if addr.Network() != "udp" {
		t.Errorf("expected network udp, got %s", addr.Network())
	}
}

func TestKCPTransport_Deadlines(t *testing.T) {
	ln, err := (&KCPTransport{}).Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()

	go func() {
		conn, err := kcp.DialWithOptions(addr, nil, 0, 0)
		if err != nil {
			return
		}
		defer conn.Close()
		conn.Write([]byte("x"))
		time.Sleep(2 * time.Second)
	}()

	ln.(*kcpListener).SetDeadline(time.Now().Add(5 * time.Second))
	srvConn, err := ln.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	defer srvConn.Close()

	// Consume the initial write
	buf := make([]byte, 64)
	srvConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	srvConn.Read(buf)

	// Now test read deadline
	srvConn.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
	_, err = srvConn.Read(buf)
	if err == nil {
		t.Error("expected timeout error, got nil")
	}
}
