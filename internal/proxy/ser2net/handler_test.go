package ser2net

import (
	"fmt"
	"io"
	"net"
	"testing"
)

type fakeSerialConn struct {
	closed bool
}

type fakeCloser struct {
	closed bool
}

func (f *fakeSerialConn) Read(_ []byte) (int, error)  { return 0, io.EOF }
func (f *fakeSerialConn) Write(p []byte) (int, error) { return len(p), nil }
func (f *fakeSerialConn) Close() error {
	f.closed = true
	return nil
}

func (f *fakeCloser) Close() error {
	f.closed = true
	return nil
}

func TestSer2NetConfigValidateRejectsUnsupportedSerialOptions(t *testing.T) {
	t.Parallel()

	cfg := Ser2NetConfig{
		Mode:    "server",
		Address: ":5000",
		Serial: SerialConfig{
			Port:     "COM3",
			BaudRate: 9600,
			DataBits: 9,
			StopBits: 3,
			Parity:   "X",
			Timeout:  0,
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want invalid serial options error")
	}
}

func TestHandlerStatsReportsSerialClosedAfterStop(t *testing.T) {
	t.Parallel()

	serial := &fakeSerialConn{}
	listener := &fakeCloser{}
	h := &Handler{
		name:     "serial-a",
		typ:      "ser2tcp",
		cfg:      Ser2NetConfig{Mode: "server", Address: ":5000", Serial: SerialConfig{Port: "COM3"}},
		serial:   serial,
		listener: listener,
	}
	h.running.Store(true)

	h.Stop()

	stats := h.Stats()
	if !serial.closed {
		t.Fatal("Stop() should close serial connection")
	}
	if !listener.closed {
		t.Fatal("Stop() should close listener")
	}
	if stats.SerialOpen {
		t.Fatal("Stats().SerialOpen = true, want false after Stop()")
	}
	if stats.Address != ":5000" {
		t.Fatalf("Stats().Address = %q, want %q", stats.Address, ":5000")
	}
}

func TestHandlerStatsIncludesSerialPort(t *testing.T) {
	t.Parallel()

	h := &Handler{
		name: "serial-b",
		typ:  "ser2udp",
		cfg: Ser2NetConfig{
			Mode:    "client",
			Address: "127.0.0.1:6000",
			Serial:  SerialConfig{Port: "COM7"},
		},
	}

	stats := h.Stats()
	if stats.SerialPort != "COM7" {
		t.Fatalf("Stats().SerialPort = %q, want %q", stats.SerialPort, "COM7")
	}
}

func TestHandlerStatsIncludesEnhancedSer2NetFields(t *testing.T) {
	t.Parallel()

	h := &Handler{
		name: "serial-enhanced",
		typ:  "ser2tcp",
		cfg: Ser2NetConfig{
			Mode:    "server",
			Address: "127.0.0.1:5000",
			Serial:  SerialConfig{Port: "COM9"},
		},
	}
	h.running.Store(true)
	h.setError(fmt.Errorf("fatal error"))
	h.setRecentError(fmt.Errorf("recent transient error"))

	stats := h.Stats()
	if stats.ServerListening {
		t.Fatal("Stats().ServerListening should be false when listener is not ready")
	}
	if stats.ClientConnected {
		t.Fatal("Stats().ClientConnected should be false in server mode without clients")
	}
	if stats.RecentError == "" {
		t.Fatal("Stats().RecentError should include transient error text")
	}
	if stats.RecentErrorAtUnixMs <= 0 {
		t.Fatal("Stats().RecentErrorAtUnixMs should be recorded")
	}
}

func TestHandlerWriteSerialFailsWhenSerialClosed(t *testing.T) {
	t.Parallel()

	h := &Handler{}
	if _, err := h.writeSerial([]byte("abc")); err == nil {
		t.Fatal("writeSerial() error = nil, want closed serial error")
	}
}

func TestStopClosesTCPClientsImmediately(t *testing.T) {
	t.Parallel()

	serverConn, _ := net.Pipe()
	h := &Handler{
		typ: "ser2tcp",
		cfg: Ser2NetConfig{Mode: "server"},
	}
	cs := newConnSet(4)
	cs.add(serverConn)
	h.setTCPClients(cs)
	h.clients.Store(1)
	h.running.Store(true)

	h.Stop()

	if got := h.clients.Load(); got != 0 {
		t.Fatalf("clients = %d, want 0 after Stop()", got)
	}
}
