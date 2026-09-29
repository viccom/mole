package tunnel

import (
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const wsUpgradePath = "/ws"

// WSTransport implements Transport for WebSocket (ws://) and secure WebSocket (wss://).
type WSTransport struct {
	TLSConfig *tls.Config
}

// NewWSTransport creates a WebSocket transport. Pass a tls.Config for WSS.
func NewWSTransport(tlsConfig *tls.Config) *WSTransport {
	return &WSTransport{TLSConfig: tlsConfig}
}

func (t *WSTransport) Listen(addr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return newWSListener(ln, t.TLSConfig), nil
}

func (t *WSTransport) Name() string {
	if t.TLSConfig != nil {
		return "wss"
	}
	return "ws"
}

// wsListener wraps an HTTP server as a net.Listener for WebSocket connections.
type wsListener struct {
	httpServer *http.Server
	connCh     chan net.Conn
	ln         net.Listener
	closed     chan struct{} // 关闭信号：send 侧经 select 感知，避免向已关闭 channel 发送 panic
	closeOnce  sync.Once
}

func newWSListener(ln net.Listener, tlsConfig *tls.Config) *wsListener {
	wl := &wsListener{
		connCh: make(chan net.Conn, 1000),
		ln:     ln,
		closed: make(chan struct{}),
	}

	mux := http.NewServeMux()
	mux.HandleFunc(wsUpgradePath, wl.handleUpgrade)
	wl.httpServer = &http.Server{
		Handler: mux,
	}

	go func() {
		var err error
		if tlsConfig != nil {
			err = wl.httpServer.ServeTLS(ln, "", "")
		} else {
			err = wl.httpServer.Serve(ln)
		}
		if err != nil && err != http.ErrServerClosed {
			slog.Error("WS HTTP server error", "error", err)
		}
	}()

	return wl
}

func (wl *wsListener) handleUpgrade(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Debug("WS upgrade failed", "remote", r.RemoteAddr, "error", err)
		return
	}
	// close(connCh) 与在途 upgrade 的 send 并发会 panic（send on closed channel）：
	// 用 closed 信号让 send 侧在关闭后走拒绝分支
	select {
	case wl.connCh <- &wsConn{conn: conn}:
	case <-wl.closed:
		conn.Close()
		slog.Warn("WS listener closed, connection rejected", "remote", conn.RemoteAddr())
	default:
		conn.Close()
		slog.Warn("WS accept queue full, connection rejected", "remote", conn.RemoteAddr())
	}
}

func (wl *wsListener) Accept() (net.Conn, error) {
	// connCh 永不 close（与在途 upgrade 的 send 竞争会 panic）：
	// 关闭态经 closed 信号通知
	select {
	case conn := <-wl.connCh:
		return conn, nil
	case <-wl.closed:
		return nil, net.ErrClosed
	}
}

func (wl *wsListener) Close() error {
	wl.closeOnce.Do(func() { close(wl.closed) })
	return wl.httpServer.Close()
}

func (wl *wsListener) Addr() net.Addr {
	return wl.ln.Addr()
}

// wsConn bridges a message-based websocket.Conn to a stream-based net.Conn.
type wsConn struct {
	conn    *websocket.Conn
	muWrite sync.Mutex
	muRead  sync.Mutex
	reader  io.Reader
}

func (c *wsConn) Read(b []byte) (int, error) {
	c.muRead.Lock()
	defer c.muRead.Unlock()

	for {
		if c.reader != nil {
			n, err := c.reader.Read(b)
			if err == io.EOF {
				c.reader = nil
				continue
			}
			return n, err
		}
		_, reader, err := c.conn.NextReader()
		if err != nil {
			return 0, toNetErr(err)
		}
		c.reader = reader
	}
}

func (c *wsConn) Write(b []byte) (int, error) {
	c.muWrite.Lock()
	defer c.muWrite.Unlock()

	w, err := c.conn.NextWriter(websocket.BinaryMessage)
	if err != nil {
		return 0, toNetErr(err)
	}
	n, err := w.Write(b)
	if err != nil {
		return n, toNetErr(err)
	}
	return n, w.Close()
}

func (c *wsConn) Close() error {
	return c.conn.Close()
}

func (c *wsConn) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

func (c *wsConn) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

func (c *wsConn) SetDeadline(t time.Time) error {
	_ = c.conn.SetReadDeadline(t)
	return c.conn.SetWriteDeadline(t)
}

func (c *wsConn) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

func (c *wsConn) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}

// toNetErr maps websocket errors to net errors for compatibility with
// net.Conn consumers that check for net.ErrClosed, etc.
func toNetErr(err error) error {
	if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
		return net.ErrClosed
	}
	if websocket.IsUnexpectedCloseError(err) {
		return net.ErrClosed
	}
	return err
}
