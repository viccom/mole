//go:build p2p

package engine

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"

	"github.com/hashicorp/yamux"

	"moleAgent_client/internal/p2p/netx"
	"moleAgent_client/internal/p2p/secure"
	"moleAgent_client/internal/p2p/session"
)

// logWriter returns the progress-log writer passed to easyp2p / secure calls。
// 按行缓冲：每个完整行经 deps.OnModeLog 转发到桌面 UI 的 modeLog 事件，让用户看到
// 打洞 / secure 升级过程（DTLS handshake、KCP handshake、↑ Sent PING 等）。OnModeLog
// 为 nil 时（如测试）丢弃。
func logWriter(deps Deps) io.Writer {
	return &lineWriter{onLine: deps.OnModeLog}
}

// lineWriter 缓冲到换行，每完整行回调 onLine（线程安全）。
// easyp2p/secure 用 fmt.Fprintf(w, "...\n") 写，可能一次 Write 多行或一行分多次，
// 故必须按 \n 切分后再转发。
type lineWriter struct {
	onLine func(string)
	mu     sync.Mutex
	buf    []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	// 防 buf 无界增长：单次超大 Write 截断；累积超限丢弃半行
	if len(p) > 64*1024 {
		p = p[:64*1024]
	}
	if len(w.buf)+len(p) > 64*1024 {
		w.buf = w.buf[:0]
	}
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(w.buf[:i]), "\r")
		w.buf = w.buf[i+1:]
		if line == "" {
			continue
		}
		if w.onLine != nil {
			w.onLine(line)
		}
	}
	return len(p), nil
}

// secureUpgrade 把打洞产物（connected net.Conn）经 secure 协商再套 yamux 多路复用，
// 返回 StreamMux。这是取代 quicUpgrade 的核心：secure.DoNegotiationContext 内部用
// netx.PacketConnWrapper + BoundUDPConn 自行处理 connected socket（不要求 unconnected），
// 因此不会破坏 NAT 映射——这正是 gonc 能用、原 quic-go 路径不能用的根本差异。
//
//   - UDP 路径（udp-v4/udp-v6）：SecureLayer="dtls" + KcpWithUDP=true（DTLS 已加密，KCP 不再加密）
//   - TCP 路径（tcp-v4/tcp-v6/lan）：SecureLayer="tls13"
//
// sharedKey（ECDHE P-256 → HKDF 32B）以 hex 编码作为 PSK：DTLS/TLS 用它派生 ECDSA 证书
// 做相互 pinning（VerifyPeerCertificateByPSK），身份认证锚定在 ECDHE 共享密钥上。
func secureUpgrade(ctx context.Context, rawconn net.Conn, sharedKey *[32]byte,
	isClient, isUDP bool, logw io.Writer) (session.StreamMux, error) {

	cfg := secure.NewNegotiationConfig()
	cfg.IsClient = isClient
	cfg.KeyType = "PSK"
	cfg.Key = hex.EncodeToString(sharedKey[:])
	cfg.InsecureSkipVerify = true // 认证由 PSK pinning 完成，非 PKI
	if isUDP {
		cfg.SecureLayer = "dtls"
		cfg.KcpWithUDP = true
		cfg.KcpEncryption = false // DTLS 已加密，KCP 层无需重复加密
	} else {
		cfg.SecureLayer = "tls13"
		cfg.KeepAlive = 30 // M7：启用 SO_KEEPALIVE，30s 探测早检测半开 TCP 连接
	}

	// TLS/DTLS PSK 模式两端都需要 PSK 派生的 ECDSA 证书（服务端 RequireAnyClientCert +
	// VerifyPeerCertificateByPSK，客户端提供 Certs 给服务端校验）。
	cert, err := secure.GenerateECDSACertificate("", cfg.Key)
	if err != nil {
		return nil, fmt.Errorf("secure gen cert: %w", err)
	}
	cfg.Certs = []tls.Certificate{*cert}

	nconn, err := secure.DoNegotiationContext(ctx, cfg, rawconn, logw)
	if err != nil {
		return nil, fmt.Errorf("secure negotiate: %w", err)
	}

	ycfg := yamux.DefaultConfig()
	// 禁用 yamux 内置 keepalive：session 层已有 HB(HeartbeatFreq=10s)+ReadTimeout(35s)
	// 做死连接检测，yamux keepalive 冗余；其 Ping/pong 经 waitForSendErr 受
	// ConnectionWriteTimeout(默认 10s) 限制，TCP-over-relay 多层透传下 ping 等不到 pong
	// 即 exitErr(ErrKeepAliveTimeout) 关闭整个 session（relay connected 后 ~9s 断的主要
	// 嫌疑）。禁用后死连接由 session 层心跳判定，yamux 不再主动关 session。
	ycfg.EnableKeepAlive = false
	// 流控窗口调大：默认 256KB 限制单流吞吐 ≈ 窗口/RTT（跨省 50ms RTT 仅 ~40Mbps 上限）。
	// 4MB 覆盖百兆×100ms RTT 的单流 BDP，让大文件/测速跑满带宽，对齐 UDP(KCP) 路径性能。
	ycfg.MaxStreamWindowSize = 4 << 20
	// 路由 yamux 内部诊断日志到 modeLog → stdout（排查用；yamux 默认写 os.Stderr 不可见）。
	ycfg.LogOutput = logw
	var sess *yamux.Session
	if isClient {
		sess, err = yamux.Client(nconn, ycfg)
	} else {
		sess, err = yamux.Server(nconn, ycfg)
	}
	if err != nil {
		nconn.Close()
		return nil, fmt.Errorf("yamux: %w", err)
	}
	fmt.Fprintf(logw, "yamux session created (isClient=%v keepAlive=30s connWriteTimeout=10s streamWindow=4MB)\n", isClient)
	return session.NewYamuxStreamMux(sess), nil
}

// unwrapUDP 从打洞产物提取底层 *net.UDPConn。
// gonc 的 Easy_P2P_MP 对 UDP 路径返回 *netx.ConnFromPacketConn（内嵌 *net.UDPConn），
// LAN/直连路径可能返回裸 *net.UDPConn。secure.DoNegotiationContext 接收 net.Conn，
// 故 UDP mode 把提取出的 *net.UDPConn（实现 net.Conn）直接喂给它。
func unwrapUDP(c net.Conn) *net.UDPConn {
	switch v := c.(type) {
	case *net.UDPConn:
		return v
	case *netx.ConnFromPacketConn:
		if u, ok := v.PacketConn.(*net.UDPConn); ok {
			return u
		}
	}
	return nil
}

// unwrapTCP extracts a *net.TCPConn from a punched conn (TCP hole-punch / LAN).
func unwrapTCP(c net.Conn) *net.TCPConn {
	if t, ok := c.(*net.TCPConn); ok {
		return t
	}
	return nil
}
