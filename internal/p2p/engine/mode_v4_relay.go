//go:build p2p

package engine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"moleAgent_client/internal/p2p/crypto"
	"moleAgent_client/internal/p2p/easyp2p"
	"moleAgent_client/internal/p2p/session"
	"moleAgent_client/internal/p2p/transport"
)

// ModeV4Relay: MQTT 换 ECDHE 密钥 → 派生 token → TCP 连 relaysrv 配对 →
// secureUpgrade(TLS PSK) + yamux。手选触发（-modes v4-relay），不进 DefaultModes。
// RelayServer 未配（-relay 空）→ err。加密端到端：relay 仅 io.Copy 透传，看不到明文。
func ModeV4Relay(ctx context.Context, deps Deps) (*session.Outcome, *[crypto.KeyLen]byte, error) {
	if deps.RelayServer == "" {
		return nil, nil, errors.New("v4-relay: no relay server configured (-relay)")
	}
	logw := logWriter(deps)

	// 1. MQTT 信令交换 ECDHE 公钥 → sharedKey（作 TLS PSK 来源）+ isClient（TLS 角色）。
	signal, err := easyp2p.NewMQTTSignalSession(ctx,
		easyp2p.MQTT_GenerateClientID(easyp2p.TopicDesc_Signal, deps.Room, 0), "", logw)
	if err != nil {
		return nil, nil, fmt.Errorf("v4-relay mqtt: %w", err)
	}
	defer signal.Close()
	sharedKey, isClient, err := easyp2p.ExchangeRelayKey(ctx, signal, deps.Room, 25*time.Second)
	if err != nil {
		return nil, nil, fmt.Errorf("v4-relay keyx: %w", err)
	}

	// 2. 从 sharedKey 派生配对 token（两端一致，relay server 零状态配对）。
	token, err := easyp2p.RelayPairingToken(sharedKey)
	if err != nil {
		return nil, nil, fmt.Errorf("v4-relay token: %w", err)
	}

	// 3. 连 relaysrv 发 RELAY <token>，等 RELAY_READY（第一端可能等满 pending 超时 120s，故 130s）。
	tcpConn, err := net.DialTimeout("tcp", deps.RelayServer, 15*time.Second)
	if err != nil {
		return nil, nil, fmt.Errorf("v4-relay dial: %w", err)
	}
	if _, err := fmt.Fprintf(tcpConn, "RELAY %s\n", token); err != nil {
		tcpConn.Close()
		return nil, nil, fmt.Errorf("v4-relay send token: %w", err)
	}
	line, err := readRelayReply(tcpConn, 130*time.Second)
	if err != nil || !strings.HasPrefix(line, "RELAY_READY") {
		tcpConn.Close()
		return nil, nil, fmt.Errorf("v4-relay pair: %s (%v)", line, err)
	}

	// 4. 调 TCP buffer（4MB）+ NoDelay，再走统一 secure 升级（TLS13 PSK pinning + yamux）。
	if t, ok := tcpConn.(*net.TCPConn); ok {
		transport.TuneTCPConn(t)
	}
	mux, err := secureUpgrade(ctx, tcpConn, sharedKey, isClient, false /*isUDP*/, logw)
	if err != nil {
		tcpConn.Close()
		return nil, nil, fmt.Errorf("v4-relay secure: %w", err)
	}
	return &session.Outcome{
		Mux:      mux,
		IsClient: isClient,
		Local:    tcpConn.LocalAddr(),
		Remote:   tcpConn.RemoteAddr(),
	}, sharedKey, nil
}

// readRelayReply 读 relay server 首行应答（RELAY_READY / ERROR），带超时。
// 逐字节读以避免 bufio 预读吞掉后续字节：配对成功后 relay 立即 io.Copy 透传对端流量，
// 用 bufio.NewReader 会把紧随 RELAY_READY 到达的 TLS ClientHello 字节缓存后丢弃，破坏握手。
func readRelayReply(conn net.Conn, timeout time.Duration) (string, error) {
	conn.SetReadDeadline(time.Now().Add(timeout))
	defer conn.SetReadDeadline(time.Time{})
	var line []byte
	one := make([]byte, 1)
	for {
		if _, err := conn.Read(one); err != nil {
			return strings.TrimSpace(string(line)), err
		}
		if one[0] == '\n' {
			return strings.TrimSpace(string(line)), nil
		}
		line = append(line, one[0])
		if len(line) > 256 {
			return strings.TrimSpace(string(line)), errors.New("relay reply too long")
		}
	}
}
