package ser2net

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestTCPServerAcceptsClient(t *testing.T) {
	t.Parallel()
	h := &Handler{
		name: "test-tcp-server",
		typ:  "ser2tcp",
		cfg: Ser2NetConfig{
			Mode:    "server",
			Address: "127.0.0.1:0",
			Serial:  SerialConfig{Port: "COM1"},
		},
		serial: &fakeSerialConn{},
	}
	h.ctx, h.cancel = context.WithCancel(context.Background())
	h.running.Store(true)

	// We don't want runTCPServer to block forever, so we run it in a goroutine.
	go h.runTCPServer()

	// Wait for listener to be set
	var ln net.Listener
	for i := 0; i < 50; i++ {
		h.listenerMu.Lock()
		if h.listener != nil {
			ln = h.listener.(net.Listener)
			h.listenerMu.Unlock()
			break
		}
		h.listenerMu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}

	if ln == nil {
		t.Fatal("listener not started")
	}

	addr := ln.Addr().String()
	conn, err := net.DialTimeout("tcp", addr, 1*time.Second)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.Close()

	// Wait for clients count to update
	for i := 0; i < 50; i++ {
		if h.clients.Load() == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if h.clients.Load() != 1 {
		t.Fatalf("expected 1 client, got %d", h.clients.Load())
	}

	h.Stop()
}

func TestTCPClientReconnectsUntilServerAvailable(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("alloc port failed: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	client := &Handler{
		name: "test-tcp-client",
		typ:  "ser2tcp",
		cfg: Ser2NetConfig{
			Mode:    "client",
			Address: addr,
			Serial:  SerialConfig{Port: "COM2"},
		},
		serial: &fakeSerialConn{},
	}
	client.ctx, client.cancel = context.WithCancel(context.Background())
	client.running.Store(true)
	go client.runTCPClient()

	time.Sleep(200 * time.Millisecond)

	server := &Handler{
		name: "test-tcp-server-late",
		typ:  "ser2tcp",
		cfg: Ser2NetConfig{
			Mode:    "server",
			Address: addr,
			Serial:  SerialConfig{Port: "COM1"},
		},
		serial: &fakeSerialConn{},
	}
	server.ctx, server.cancel = context.WithCancel(context.Background())
	server.running.Store(true)
	go server.runTCPServer()

	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if server.clients.Load() == 1 {
			server.Stop()
			client.Stop()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	server.Stop()
	client.Stop()
	t.Fatalf("expected server to have 1 client after retry, got %d", server.clients.Load())
}

func TestTCPClientKeepsRunningDuringReconnectWindow(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer ln.Close()

	h := &Handler{
		name: "test-tcp-running-flag",
		typ:  "ser2tcp",
		cfg: Ser2NetConfig{
			Mode:    "client",
			Address: ln.Addr().String(),
			Serial:  SerialConfig{Port: "COM1"},
		},
		serial: &fakeSerialConn{},
	}
	h.ctx, h.cancel = context.WithCancel(context.Background())
	h.running.Store(true)

	go h.runTCPClient()
	defer h.Stop()

	accepted := make(chan net.Conn, 1)
	go func() {
		c, e := ln.Accept()
		if e == nil {
			accepted <- c
		}
	}()

	var serverConn net.Conn
	select {
	case serverConn = <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("tcp client did not connect to test server")
	}
	_ = serverConn.Close()

	// During reconnect sleep window, handler should still be considered running.
	time.Sleep(150 * time.Millisecond)
	if !h.IsRunning() {
		t.Fatal("IsRunning() = false during reconnect window, want true")
	}
}
