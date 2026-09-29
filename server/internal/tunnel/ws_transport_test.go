package tunnel

import (
	"bytes"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// ---------------------------------------------------------------------------
// WSTransport
// ---------------------------------------------------------------------------

func TestWSTransport_Name(t *testing.T) {
	if got := (&WSTransport{}).Name(); got != "ws" {
		t.Errorf("expected ws, got %s", got)
	}
	if got := (&WSTransport{TLSConfig: &tls.Config{}}).Name(); got != "wss" {
		t.Errorf("expected wss, got %s", got)
	}
}

// ---------------------------------------------------------------------------
// wsConn stream bridge
// ---------------------------------------------------------------------------

func TestWSConn_ReadWrite(t *testing.T) {
	srvConn, cliConn := wsPipe(t)
	defer srvConn.Close()
	defer cliConn.Close()

	data := []byte("hello world")
	go func() {
		w, _ := srvConn.NextWriter(websocket.BinaryMessage)
		w.Write(data)
		w.Close()
	}()

	buf := make([]byte, 64)
	n, err := cliConn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf[:n]) != string(data) {
		t.Errorf("expected %q, got %q", data, buf[:n])
	}
}

func TestWSConn_MultipleMessages(t *testing.T) {
	srvConn, cliConn := wsPipe(t)
	defer srvConn.Close()
	defer cliConn.Close()

	go func() {
		for _, msg := range []string{"aaa", "bbb", "ccc"} {
			w, _ := srvConn.NextWriter(websocket.BinaryMessage)
			w.Write([]byte(msg))
			w.Close()
		}
	}()

	var buf bytes.Buffer
	tmp := make([]byte, 64)
	for i := 0; i < 3; i++ {
		n, err := cliConn.Read(tmp)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		buf.Write(tmp[:n])
	}

	got := buf.String()
	if got != "aaabbbccc" {
		t.Errorf("expected 'aaabbbccc', got %q", got)
	}
}

func TestWSConn_WriteReadRoundTrip(t *testing.T) {
	srvConn, cliConn := wsPipe(t)
	defer srvConn.Close()
	defer cliConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, msg, err := srvConn.ReadMessage()
		if err != nil {
			t.Errorf("server read: %v", err)
			return
		}
		if string(msg) != "ping" {
			t.Errorf("expected 'ping', got %q", msg)
		}
		srvConn.WriteMessage(websocket.BinaryMessage, []byte("pong"))
	}()

	_, err := cliConn.Write([]byte("ping"))
	if err != nil {
		t.Fatalf("client write: %v", err)
	}
	<-done

	buf := make([]byte, 64)
	n, err := cliConn.Read(buf)
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if string(buf[:n]) != "pong" {
		t.Errorf("expected 'pong', got %q", buf[:n])
	}
}

func TestWSConn_Addresses(t *testing.T) {
	_, cliConn := wsPipe(t)
	defer cliConn.Close()

	if cliConn.LocalAddr() == nil {
		t.Error("LocalAddr should not be nil")
	}
	if cliConn.RemoteAddr() == nil {
		t.Error("RemoteAddr should not be nil")
	}
}

func TestWSConn_Deadlines(t *testing.T) {
	_, cliConn := wsPipe(t)
	defer cliConn.Close()

	if err := cliConn.SetDeadline(time.Time{}); err != nil {
		t.Errorf("SetDeadline: %v", err)
	}
	if err := cliConn.SetReadDeadline(time.Time{}); err != nil {
		t.Errorf("SetReadDeadline: %v", err)
	}
	if err := cliConn.SetWriteDeadline(time.Time{}); err != nil {
		t.Errorf("SetWriteDeadline: %v", err)
	}
}

func TestWSConn_ReadTimeout(t *testing.T) {
	_, cliConn := wsPipe(t)
	defer cliConn.Close()

	cliConn.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
	buf := make([]byte, 64)
	_, err := cliConn.Read(buf)
	if err == nil {
		t.Error("expected timeout error, got nil")
	}
}

// ---------------------------------------------------------------------------
// wsListener integration
// ---------------------------------------------------------------------------

func TestWSListener_Accept(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	wl := newWSListener(ln, nil)
	defer wl.Close()

	go func() {
		time.Sleep(50 * time.Millisecond)
		conn, _, err := websocket.DefaultDialer.Dial("ws://"+ln.Addr().String()+wsUpgradePath, nil)
		if err != nil {
			t.Errorf("dial: %v", err)
			return
		}
		conn.Close()
	}()

	conn, err := wl.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	conn.Close()
}

func TestWSListener_RejectNonWS(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	wl := newWSListener(ln, nil)
	defer wl.Close()

	resp, err := http.Get("http://" + ln.Addr().String() + wsUpgradePath)
	if err != nil {
		t.Fatalf("http get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusSwitchingProtocols {
		t.Error("plain HTTP should not upgrade")
	}
}

// ---------------------------------------------------------------------------
// toNetErr
// ---------------------------------------------------------------------------

func TestToNetErr_NormalClose(t *testing.T) {
	err := toNetErr(&websocket.CloseError{Code: websocket.CloseNormalClosure})
	if err != net.ErrClosed {
		t.Errorf("expected net.ErrClosed for normal close, got %v", err)
	}
}

func TestToNetErr_Other(t *testing.T) {
	err := toNetErr(io.ErrUnexpectedEOF)
	if err != io.ErrUnexpectedEOF {
		t.Errorf("expected original error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// wsPipe creates a connected pair: (*websocket.Conn, *wsConn) for testing.
// srvConn is the server side (raw websocket.Conn), cliConn is the client side
// wrapped as a net.Conn via wsConn.
func wsPipe(t *testing.T) (*websocket.Conn, *wsConn) {
	t.Helper()

	srvConnCh := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		srvConnCh <- conn
	}))
	t.Cleanup(srv.Close)

	wsURL := "ws://" + strings.TrimPrefix(srv.URL, "http://") + "/"
	clientRaw, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	var srvConn *websocket.Conn
	select {
	case srvConn = <-srvConnCh:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for server conn")
	}

	t.Cleanup(func() { srvConn.Close() })
	t.Cleanup(func() { clientRaw.Close() })

	return srvConn, &wsConn{conn: clientRaw}
}
