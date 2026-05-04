package transport

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	kcp "github.com/xtaci/kcp-go/v5"
)

// ---------------------------------------------------------------------------
// WS Dialer tests
// ---------------------------------------------------------------------------

func TestWSDialer_ConnectAndEcho(t *testing.T) {
	up := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _ := up.Upgrade(w, r, nil)
		defer conn.Close()
		for {
			msgType, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			conn.WriteMessage(msgType, data)
		}
	}))
	defer srv.Close()

	dial := NewWSDialer(WSDialerConfig{})
	conn, err := dial(context.Background(), strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	msg := []byte("hello ws")
	conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write(msg); err != nil {
		t.Fatalf("write: %v", err)
	}

	buf := make([]byte, 64)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf[:n]) != string(msg) {
		t.Errorf("expected %q, got %q", msg, buf[:n])
	}
}

func TestWSDialer_MultipleMessages(t *testing.T) {
	up := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _ := up.Upgrade(w, r, nil)
		defer conn.Close()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			conn.WriteMessage(websocket.BinaryMessage, data)
		}
	}))
	defer srv.Close()

	dial := NewWSDialer(WSDialerConfig{})
	conn, err := dial(context.Background(), strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	for _, msg := range []string{"aaa", "bbb", "ccc"} {
		conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
		conn.Write([]byte(msg))
		buf := make([]byte, 64)
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if string(buf[:n]) != msg {
			t.Errorf("expected %q, got %q", msg, buf[:n])
		}
	}
}

func TestWSDialer_Deadlines(t *testing.T) {
	up := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _ := up.Upgrade(w, r, nil)
		defer conn.Close()
		time.Sleep(5 * time.Second)
	}))
	defer srv.Close()

	dial := NewWSDialer(WSDialerConfig{})
	conn, err := dial(context.Background(), strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
	buf := make([]byte, 64)
	_, err = conn.Read(buf)
	if err == nil {
		t.Error("expected timeout error, got nil")
	}
}

// ---------------------------------------------------------------------------
// KCP Dialer tests
// ---------------------------------------------------------------------------

func TestKCPDialer_ConnectAndEcho(t *testing.T) {
	ln, err := kcp.ListenWithOptions("127.0.0.1:0", nil, 0, 0)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()

	go func() {
		ln.SetDeadline(time.Now().Add(5 * time.Second))
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 64)
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		conn.Write(buf[:n])
		time.Sleep(2 * time.Second)
	}()

	dial := NewKCPDialer(KCPDialerConfig{})
	conn, err := dial(context.Background(), addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	msg := []byte("hello kcp")
	conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	conn.Write(msg)

	buf := make([]byte, 64)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf[:n]) != string(msg) {
		t.Errorf("expected %q, got %q", msg, buf[:n])
	}
}

func TestKCPDialer_WithEncryption(t *testing.T) {
	key := "test-secret-key-1234567890"
	h := sha256.Sum256([]byte(key))
	block, _ := kcp.NewAESBlockCrypt(h[:])

	ln, err := kcp.ListenWithOptions("127.0.0.1:0", block, 0, 0)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()

	go func() {
		ln.SetDeadline(time.Now().Add(5 * time.Second))
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 64)
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, _ := conn.Read(buf)
		conn.Write(buf[:n])
		time.Sleep(2 * time.Second)
	}()

	dial := NewKCPDialer(KCPDialerConfig{Key: key})
	conn, err := dial(context.Background(), addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	msg := []byte("encrypted kcp")
	conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	conn.Write(msg)

	buf := make([]byte, 64)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf[:n]) != string(msg) {
		t.Errorf("expected %q, got %q", msg, buf[:n])
	}
}

func TestKCPDialer_WithFEC(t *testing.T) {
	cfg := KCPDialerConfig{DataShards: 10, ParityShards: 3}

	ln, err := kcp.ListenWithOptions("127.0.0.1:0", nil, cfg.DataShards, cfg.ParityShards)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()

	go func() {
		ln.SetDeadline(time.Now().Add(5 * time.Second))
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 64)
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, _ := conn.Read(buf)
		conn.Write(buf[:n])
		time.Sleep(2 * time.Second)
	}()

	dial := NewKCPDialer(cfg)
	conn, err := dial(context.Background(), addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	msg := []byte("fec kcp")
	conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	conn.Write(msg)

	buf := make([]byte, 64)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf[:n]) != string(msg) {
		t.Errorf("expected %q, got %q", msg, buf[:n])
	}
}

func TestKCPDialer_Deadline(t *testing.T) {
	ln, err := kcp.ListenWithOptions("127.0.0.1:0", nil, 0, 0)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()

	go func() {
		ln.SetDeadline(time.Now().Add(5 * time.Second))
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.Write([]byte("x"))
		time.Sleep(2 * time.Second)
	}()

	dial := NewKCPDialer(KCPDialerConfig{})
	conn, err := dial(context.Background(), addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Consume initial data
	buf := make([]byte, 64)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	conn.Read(buf)

	// Now test read deadline
	conn.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
	_, err = conn.Read(buf)
	if err == nil {
		t.Error("expected timeout error, got nil")
	}
}
