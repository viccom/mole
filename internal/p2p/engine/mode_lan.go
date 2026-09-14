//go:build p2p

package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"moleAgent_client/internal/p2p/crypto"
	"moleAgent_client/internal/p2p/easyp2p"
	"moleAgent_client/internal/p2p/session"
	"moleAgent_client/internal/p2p/transport"
)

// ModeLAN: same-subnet UDP multicast discovery (<200ms), TCP direct connect.
// Bypasses NAT traversal entirely when both peers share a LAN。secure(TLS)+yamux 升级。
func ModeLAN(ctx context.Context, deps Deps) (*session.Outcome, *[crypto.KeyLen]byte, error) {
	info, err := easyp2p.Easy_P2P_LAN(ctx, deps.Room, "tcp", 10*time.Second, logWriter(deps))
	if err != nil {
		return nil, nil, fmt.Errorf("lan: %w", err)
	}
	if len(info.Conns) == 0 {
		return nil, nil, errors.New("lan: no conn returned")
	}
	tcpConn := unwrapTCP(info.Conns[0])
	if tcpConn == nil {
		// 对端偏好 UDP 时协商降级为 UDP 连接：必须就地关闭，
		// 否则每次 lan 尝试泄漏一个 NAT 映射 socket（复审 F13）
		for _, c := range info.Conns {
			_ = c.Close()
		}
		return nil, nil, errors.New("lan: conn is not TCP")
	}
	transport.TuneTCPConn(tcpConn)

	isClient := info.IsClient
	mux, err := secureUpgrade(ctx, tcpConn, &info.SharedKey, isClient, false, logWriter(deps))
	if err != nil {
		tcpConn.Close()
		return nil, nil, fmt.Errorf("lan secure: %w", err)
	}
	return &session.Outcome{
		Mux:      mux,
		IsClient: isClient,
		Local:    tcpConn.LocalAddr(),
		Remote:   tcpConn.RemoteAddr(),
	}, &info.SharedKey, nil
}
