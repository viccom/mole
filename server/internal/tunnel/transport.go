package tunnel

import (
	"crypto/tls"
	"net"
)

// Transport abstracts the underlying network transport for the control server.
// Implementations: TCPTransport (TCP/TLS), future: KCP, WebSocket, etc.
type Transport interface {
	// Listen creates a listener on the given address using this transport.
	// The returned listener should fully configure each accepted connection
	// (e.g. TCP_NODELAY for TCP, window sizes for KCP) so that the caller
	// receives ready-to-use connections with no transport-specific setup.
	Listen(addr string) (net.Listener, error)
	// Name returns the transport identifier for logging (e.g. "tcp", "tls", "kcp").
	Name() string
}

// TCPTransport implements Transport for plain TCP with optional TLS.
type TCPTransport struct {
	TLSConfig *tls.Config
}

// NewTCPTransport creates a TCP transport. Pass a tls.Config to enable TLS.
func NewTCPTransport(tlsConfig *tls.Config) *TCPTransport {
	return &TCPTransport{TLSConfig: tlsConfig}
}

func (t *TCPTransport) Listen(addr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	// Wrap at TCP level first so SetNoDelay applies before TLS handshake
	ln = &tcpListener{Listener: ln}
	if t.TLSConfig != nil {
		ln = tls.NewListener(ln, t.TLSConfig)
	}
	return ln, nil
}

func (t *TCPTransport) Name() string {
	if t.TLSConfig != nil {
		return "tls"
	}
	return "tcp"
}

// tcpListener wraps a net.Listener to configure each accepted TCP connection
// with transport-level optimizations before it reaches the business layer.
type tcpListener struct {
	net.Listener
}

func (l *tcpListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}
	return conn, nil
}
