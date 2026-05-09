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
