package transport

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	kcp "github.com/xtaci/kcp-go/v5"

	"mole/shared/proto"
)

// ---------------------------------------------------------------------------
// WS / WSS Dialer
// ---------------------------------------------------------------------------

// WSDialerConfig holds WebSocket client configuration.
type WSDialerConfig struct {
	TLSConfig *tls.Config // nil for ws://, non-nil for wss://
}

// NewWSDialer returns a DialFunc that connects via WebSocket.
func NewWSDialer(cfg WSDialerConfig) DialFunc {
	return func(ctx context.Context, addr string) (net.Conn, error) {
		var url string
		tlsConfig := cfg.TLSConfig
		if strings.HasPrefix(addr, "ws://") || strings.HasPrefix(addr, "wss://") {
			url = addr
			if strings.HasPrefix(addr, "wss://") && tlsConfig == nil {
				tlsConfig = &tls.Config{InsecureSkipVerify: true}
				// 配置未启用 tls 时对 wss:// 只能跳过证书校验（兼容自签名部署），
				// 但必须让用户知情：中间人可中继/劫持会话（proof 认证后节点
				// token 不再上线，proof 绑定一次性 challenge 亦不可重放，但流量
				// 机密性仍依赖 TLS——跳过校验即放弃这层防护）
				log.Printf("警告: %s 为 wss:// 地址但配置未启用 tls，已跳过证书校验（存在中间人风险）", addr)
			}
		} else {
			scheme := "ws"
			if tlsConfig != nil {
				scheme = "wss"
			}
			// 升级路径单源至 mole/shared/proto（与服务端 wsUpgradePath 一致）
			url = fmt.Sprintf("%s://%s%s", scheme, addr, proto.WSUpgradePath)
		}
		dialer := websocket.Dialer{
			HandshakeTimeout: DefaultConnectTimeout,
			TLSClientConfig:  tlsConfig,
		}

		wsConn, _, err := dialer.DialContext(ctx, url, nil)
		if err != nil {
			return nil, fmt.Errorf("ws dial %s: %w", url, err)
		}

		return &wsClientConn{conn: wsConn}, nil
	}
}

// wsClientConn bridges message-based websocket.Conn to stream-based net.Conn.
// Independent implementation for the client side.
type wsClientConn struct {
	conn    *websocket.Conn
	muWrite sync.Mutex
	muRead  sync.Mutex
	reader  io.Reader
}

func (c *wsClientConn) Read(b []byte) (int, error) {
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
			return 0, wsToNetErr(err)
		}
		c.reader = reader
	}
}

func (c *wsClientConn) Write(b []byte) (int, error) {
	c.muWrite.Lock()
	defer c.muWrite.Unlock()

	w, err := c.conn.NextWriter(websocket.BinaryMessage)
	if err != nil {
		return 0, wsToNetErr(err)
	}
	n, err := w.Write(b)
	if err != nil {
		return n, wsToNetErr(err)
	}
	return n, w.Close()
}

func (c *wsClientConn) Close() error {
	return c.conn.Close()
}

func (c *wsClientConn) LocalAddr() net.Addr  { return c.conn.LocalAddr() }
func (c *wsClientConn) RemoteAddr() net.Addr { return c.conn.RemoteAddr() }

func (c *wsClientConn) SetDeadline(t time.Time) error {
	_ = c.conn.SetReadDeadline(t)
	return c.conn.SetWriteDeadline(t)
}

func (c *wsClientConn) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

func (c *wsClientConn) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}

func wsToNetErr(err error) error {
	if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
		return net.ErrClosed
	}
	if websocket.IsUnexpectedCloseError(err) {
		return net.ErrClosed
	}
	return err
}

// ---------------------------------------------------------------------------
// KCP Dialer
// ---------------------------------------------------------------------------

// KCPDialerConfig holds KCP client configuration.
type KCPDialerConfig struct {
	Key          string // Encryption key (empty = no encryption)
	DataShards   int    // FEC data shards (0 = disable FEC)
	ParityShards int    // FEC parity shards
	NoDelay      int    // 0: default, 1: enable nodelay
	Interval     int    // ACK interval ms (default 40, recommend 10)
	Resend       int    // Fast retransmit (0: default, 2 recommended)
	NoCongestion int    // 0: default, 1: disable congestion control
	SendWindow   int    // Send window (0: use default)
	RecvWindow   int    // Recv window (0: use default)
}

// NewKCPDialer returns a DialFunc that connects via KCP.
func NewKCPDialer(cfg KCPDialerConfig) DialFunc {
	if cfg.DataShards < 0 {
		cfg.DataShards = 0
	}
	if cfg.ParityShards < 0 {
		cfg.ParityShards = 0
	}

	return func(ctx context.Context, addr string) (net.Conn, error) {
		block, err := kcpBlockCrypt(cfg.Key)
		if err != nil {
			return nil, err
		}

		type dialResult struct {
			sess *kcp.UDPSession
			err  error
		}
		ch := make(chan dialResult, 1)
		go func() {
			sess, err := kcp.DialWithOptions(addr, block, cfg.DataShards, cfg.ParityShards)
			ch <- dialResult{sess, err}
		}()

		select {
		case <-ctx.Done():
			go func() {
				if r := <-ch; r.sess != nil {
					r.sess.Close()
				}
			}()
			return nil, ctx.Err()
		case r := <-ch:
			if r.err != nil {
				return nil, fmt.Errorf("kcp dial %s: %w", addr, r.err)
			}
			sess := r.sess
			applyKCPSessionTuning(sess, cfg)
			log.Printf("  kcp: dial OK (conv=%d, local=%s)", sess.GetConv(), sess.LocalAddr())
			// Send a probe byte to trigger server Accept (KCP Accept only returns after first data)
			if err := sess.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
				sess.Close()
				return nil, fmt.Errorf("kcp probe deadline: %w", err)
			}
			if _, err := sess.Write([]byte{0}); err != nil {
				sess.Close()
				return nil, fmt.Errorf("kcp probe write: %w", err)
			}
			log.Println("  kcp: probe byte sent")
			sess.SetWriteDeadline(time.Time{})
			return sess, nil
		}
	}
}

func applyKCPSessionTuning(sess *kcp.UDPSession, cfg KCPDialerConfig) {
	interval := cfg.Interval
	if interval <= 0 {
		interval = 10
	}
	resend := cfg.Resend
	if resend <= 0 {
		resend = 2
	}
	nc := cfg.NoCongestion
	if nc <= 0 {
		nc = 1
	}
	nodelay := cfg.NoDelay
	if nodelay <= 0 {
		nodelay = 1
	}
	sess.SetNoDelay(nodelay, interval, resend, nc)

	if cfg.SendWindow > 0 || cfg.RecvWindow > 0 {
		sndwnd := cfg.SendWindow
		if sndwnd <= 0 {
			sndwnd = 2048
		}
		rcvwnd := cfg.RecvWindow
		if rcvwnd <= 0 {
			rcvwnd = 2048
		}
		sess.SetWindowSize(sndwnd, rcvwnd)
	}
}

func kcpBlockCrypt(key string) (kcp.BlockCrypt, error) {
	if key == "" {
		return nil, nil
	}
	h := sha256.Sum256([]byte(key))
	return kcp.NewAESBlockCrypt(h[:])
}
