package ser2net

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestUDPServerTracksPeers(t *testing.T) {
	t.Parallel()
	h := &Handler{
		name: "test-udp-server",
		typ:  "ser2udp",
		cfg: Ser2NetConfig{
			Mode:    "server",
			Address: "127.0.0.1:0",
			Serial:  SerialConfig{Port: "COM1"},
		},
		serial: &fakeSerialConn{},
	}
	h.ctx, h.cancel = context.WithCancel(context.Background())
	h.running.Store(true)

	go h.runUDPServer()

	// Wait for listener to be set
	var conn *net.UDPConn
	for i := 0; i < 50; i++ {
		h.listenerMu.Lock()
		if h.listener != nil {
			conn = h.listener.(*net.UDPConn)
			h.listenerMu.Unlock()
			break
		}
		h.listenerMu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}

	if conn == nil {
		t.Fatal("udp listener not started")
	}

	addr := conn.LocalAddr().String()
	raddr, _ := net.ResolveUDPAddr("udp", addr)

	client, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		t.Fatalf("failed to dial udp: %v", err)
	}
	defer client.Close()

	// Send some data to trigger peer tracking
	client.Write([]byte("hello"))

	// Wait for peer to be tracked
	for i := 0; i < 50; i++ {
		if h.clients.Load() == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if h.clients.Load() != 1 {
		t.Fatalf("expected 1 peer, got %d", h.clients.Load())
	}

	h.Stop()
}
