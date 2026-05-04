package tunnel

import (
	"crypto/sha256"
	"log/slog"
	"net"

	kcp "github.com/xtaci/kcp-go/v5"
)

// KCPConfig holds KCP-specific tuning parameters.
type KCPConfig struct {
	Key          string // Encryption key (empty = no encryption)
	DataShards   int    // FEC data shards (0 to disable FEC)
	ParityShards int    // FEC parity shards
	NoDelay      int    // 0: default, 1: enable nodelay
	Interval     int    // ACK interval in ms (default 40, recommend 10 for low latency)
	Resend       int    // Fast retransmit threshold (0: default, 2 recommended)
	NoCongestion int    // 0: default, 1: disable congestion control
	SendWindow   int    // Send window size (0: use kcp-go default)
	RecvWindow   int    // Receive window size (0: use kcp-go default)
}

// KCPTransport implements Transport for KCP protocol.
type KCPTransport struct {
	Config KCPConfig
}

// NewKCPTransport creates a KCP transport with the given configuration.
func NewKCPTransport(cfg KCPConfig) *KCPTransport {
	if cfg.DataShards < 0 {
		cfg.DataShards = 0
	}
	if cfg.ParityShards < 0 {
		cfg.ParityShards = 0
	}
	return &KCPTransport{Config: cfg}
}

func (t *KCPTransport) Listen(addr string) (net.Listener, error) {
	block, err := t.blockCrypt()
	if err != nil {
		return nil, err
	}

	ln, err := kcp.ListenWithOptions(addr, block, t.Config.DataShards, t.Config.ParityShards)
	if err != nil {
		return nil, err
	}

	if err := ln.SetReadBuffer(4194304); err != nil {
		slog.Warn("KCP SetReadBuffer failed (system limit may be lower)", "error", err)
	}
	if err := ln.SetWriteBuffer(4194304); err != nil {
		slog.Warn("KCP SetWriteBuffer failed (system limit may be lower)", "error", err)
	}

	slog.Info("KCP listener created",
		"addr", addr,
		"dataShards", t.Config.DataShards,
		"parityShards", t.Config.ParityShards,
		"encrypted", t.Config.Key != "",
	)

	return &kcpListener{Listener: ln, cfg: t.Config}, nil
}

func (t *KCPTransport) Name() string {
	return "kcp"
}

func (t *KCPTransport) blockCrypt() (kcp.BlockCrypt, error) {
	if t.Config.Key == "" {
		return nil, nil
	}
	h := sha256.Sum256([]byte(t.Config.Key))
	return kcp.NewAESBlockCrypt(h[:])
}

// kcpListener wraps *kcp.Listener to apply session-level tuning on each accepted connection.
type kcpListener struct {
	*kcp.Listener
	cfg KCPConfig
}

func (l *kcpListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	if sess, ok := conn.(*kcp.UDPSession); ok {
		interval := l.cfg.Interval
		if interval <= 0 {
			interval = 10
		}
		resend := l.cfg.Resend
		if resend <= 0 {
			resend = 2
		}
		nc := l.cfg.NoCongestion
		if nc <= 0 {
			nc = 1
		}
		nodelay := l.cfg.NoDelay
		if nodelay <= 0 {
			nodelay = 1
		}
		sess.SetNoDelay(nodelay, interval, resend, nc)
		if l.cfg.SendWindow > 0 || l.cfg.RecvWindow > 0 {
			sndwnd := l.cfg.SendWindow
			if sndwnd <= 0 {
				sndwnd = 2048
			}
			rcvwnd := l.cfg.RecvWindow
			if rcvwnd <= 0 {
				rcvwnd = 2048
			}
			sess.SetWindowSize(sndwnd, rcvwnd)
		}
	}

	return conn, nil
}
