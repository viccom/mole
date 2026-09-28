package tunnel

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

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

	kl := &kcpListener{Listener: ln, cfg: t.Config}
	go kl.snmpMonitor(context.Background())
	return kl, nil
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
// It also tracks active connections by remote address to close stale sessions on reconnect.
type kcpListener struct {
	*kcp.Listener
	cfg       KCPConfig
	connMap   sync.Map // remoteAddr → *trackedConn
	connCount atomic.Int64
}

func (l *kcpListener) snmpMonitor(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	var lastInPkts, lastInErrs, lastInCsumErrors, lastKCPInErrors uint64
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snmp := kcp.DefaultSnmp
			inPkts := atomic.LoadUint64(&snmp.InPkts)
			inErrs := atomic.LoadUint64(&snmp.InErrs)
			inCsumErrors := atomic.LoadUint64(&snmp.InCsumErrors)
			kcpInErrors := atomic.LoadUint64(&snmp.KCPInErrors)
			inBytes := atomic.LoadUint64(&snmp.InBytes)
			outPkts := atomic.LoadUint64(&snmp.OutPkts)
			outBytes := atomic.LoadUint64(&snmp.OutBytes)
			currEstab := atomic.LoadUint64(&snmp.CurrEstab)

			deltaPkts := inPkts - lastInPkts
			deltaErrs := inErrs - lastInErrs
			deltaCsum := inCsumErrors - lastInCsumErrors
			deltaKCP := kcpInErrors - lastKCPInErrors

			slog.Info("KCP SNMP",
				"inPkts", deltaPkts, "inBytes", inBytes,
				"outPkts", outPkts, "outBytes", outBytes,
				"inErrs", deltaErrs, "inCsumErrs", deltaCsum,
				"kcpInErrs", deltaKCP,
				"currEstab", currEstab, "active", l.connCount.Load(),
			)

			lastInPkts = inPkts
			lastInErrs = inErrs
			lastInCsumErrors = inCsumErrors
			lastKCPInErrors = kcpInErrors
		}
	}
}

func (l *kcpListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	remoteAddr := conn.RemoteAddr().String()

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

	// Close stale connection from the same remote address (safety net for
	// cases where kcp-go's internal session replacement doesn't trigger).
	if old, loaded := l.connMap.LoadAndDelete(remoteAddr); loaded {
		if tc, ok := old.(*trackedConn); ok {
			slog.Warn("KCP closing stale connection for same remote addr",
				"remote", remoteAddr, "staleAge", tc.age())
			tc.Conn.Close()
		}
	}

	tc := &trackedConn{Conn: conn, remoteAddr: remoteAddr, listener: l, created: time.Now().UnixNano()}
	l.connMap.Store(remoteAddr, tc)
	l.connCount.Add(1)
	slog.Info("KCP accepted", "remote", remoteAddr, "active", l.connCount.Load())

	return tc, nil
}

// removeConn removes a tracked connection. Called by trackedConn.Close().
func (l *kcpListener) removeConn(tc *trackedConn) {
	if l.connMap.CompareAndDelete(tc.remoteAddr, tc) {
		l.connCount.Add(-1)
	}
}

// trackedConn wraps net.Conn to notify kcpListener on close.
type trackedConn struct {
	net.Conn
	remoteAddr string
	listener   *kcpListener
	created    int64 // unix nano
}

func (c *trackedConn) Close() error {
	c.listener.removeConn(c)
	return c.Conn.Close()
}

func (c *trackedConn) age() int64 {
	// 连接建立至今的秒数（诊断同源地址陈旧连接的存活时长）
	return int64(time.Since(time.Unix(0, c.created)).Seconds())
}
