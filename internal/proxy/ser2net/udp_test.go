package ser2net

import (
	"context"
	"io"
	"net"
	"sync/atomic"
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

type countingSerial struct {
	writes int32
}

func (s *countingSerial) Read(_ []byte) (int, error) { return 0, nil }
func (s *countingSerial) Write(p []byte) (int, error) {
	atomic.AddInt32(&s.writes, 1)
	return len(p), nil
}
func (s *countingSerial) Close() error { return nil }

func TestUDPServerProbeLearnsPeerWithoutWritingSerial(t *testing.T) {
	t.Parallel()
	serial := &countingSerial{}
	h := &Handler{
		name: "test-udp-server-probe",
		typ:  "ser2udp",
		cfg: Ser2NetConfig{
			Mode:    "server",
			Address: "127.0.0.1:0",
			Serial:  SerialConfig{Port: "COM1"},
		},
		serial: serial,
	}
	h.ctx, h.cancel = context.WithCancel(context.Background())
	h.running.Store(true)

	go h.runUDPServer()

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

	raddr, _ := net.ResolveUDPAddr("udp", conn.LocalAddr().String())
	client, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		t.Fatalf("failed to dial udp: %v", err)
	}
	defer client.Close()

	if _, err := client.Write(udpProbePayload); err != nil {
		t.Fatalf("failed to send udp probe: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	if got := h.clients.Load(); got != 1 {
		t.Fatalf("clients = %d, want 1 after probe", got)
	}
	if wrote := atomic.LoadInt32(&serial.writes); wrote != 0 {
		t.Fatalf("serial writes = %d, want 0 for probe packet", wrote)
	}

	h.Stop()
}

type timeoutNetError struct {
	d time.Duration
}

func (e timeoutNetError) Error() string   { return "i/o timeout" }
func (e timeoutNetError) Timeout() bool   { return true }
func (e timeoutNetError) Temporary() bool { return true }

type eofThenDataSerial struct {
	calls int32
}

func (s *eofThenDataSerial) Read(p []byte) (int, error) {
	n := atomic.AddInt32(&s.calls, 1)
	switch n {
	case 1:
		return 0, io.EOF
	case 2:
		copy(p, []byte("ping"))
		return 4, nil
	default:
		time.Sleep(10 * time.Millisecond)
		return 0, timeoutNetError{10 * time.Millisecond}
	}
}
func (s *eofThenDataSerial) Write(p []byte) (int, error) { return len(p), nil }
func (s *eofThenDataSerial) Close() error                { return nil }

func TestUDPServerSerialEOFDoesNotStopForwarding(t *testing.T) {
	t.Parallel()
	h := &Handler{
		name: "test-udp-server-eof",
		typ:  "ser2udp",
		cfg: Ser2NetConfig{
			Mode:    "server",
			Address: "127.0.0.1:0",
			Serial:  SerialConfig{Port: "COM1"},
		},
		serial: &eofThenDataSerial{},
	}
	h.ctx, h.cancel = context.WithCancel(context.Background())
	h.running.Store(true)

	go h.runUDPServer()

	var server *net.UDPConn
	for i := 0; i < 50; i++ {
		h.listenerMu.Lock()
		if h.listener != nil {
			if c, ok := h.listener.(*net.UDPConn); ok {
				server = c
			}
			h.listenerMu.Unlock()
			if server != nil {
				break
			}
		} else {
			h.listenerMu.Unlock()
		}
		time.Sleep(10 * time.Millisecond)
	}
	if server == nil {
		t.Fatal("udp listener not started")
	}

	remoteAddr, _ := net.ResolveUDPAddr("udp", server.LocalAddr().String())
	client, err := net.DialUDP("udp", nil, remoteAddr)
	if err != nil {
		t.Fatalf("failed to dial udp: %v", err)
	}
	defer client.Close()

	if _, err := client.Write([]byte("hello")); err != nil {
		t.Fatalf("failed to send hello: %v", err)
	}

	buf := make([]byte, 16)
	_ = client.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	n, _, err := client.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("expected forwarded packet after transient EOF, got error: %v", err)
	}
	if got := string(buf[:n]); got != "ping" {
		t.Fatalf("forwarded packet = %q, want %q", got, "ping")
	}

	h.Stop()
}
