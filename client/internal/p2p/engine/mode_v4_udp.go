//go:build p2p

package engine

import (
	"context"
	"errors"
	"fmt"

	"moleAgent_client/internal/p2p/crypto"
	"moleAgent_client/internal/p2p/easyp2p"
	"moleAgent_client/internal/p2p/session"
	"moleAgent_client/internal/p2p/transport"
)

// ModeV4UDP: IPv4 UDP hole punch (NAT-aware) → secure 升级（DTLS+KCP）+ yamux。
//
// gonc 打洞产物是 connected *net.UDPConn（已建立 NAT 映射）；secureUpgrade 内部经
// secure.DoNegotiationContext 用 PacketConnWrapper+BoundUDPConn 处理 connected socket，
// 不破坏 NAT 映射——这是取代 quic-go（要求 unconnected、重绑破坏映射）的关键。
// 不再需要 rebind：secure 包原生消费 connected conn。
func ModeV4UDP(ctx context.Context, deps Deps) (*session.Outcome, *[crypto.KeyLen]byte, error) {
	info, err := easyp2p.Easy_P2P_MP(ctx, "udp4", "", deps.Room, false, nil, logWriter(deps), nil)
	if err != nil {
		return nil, nil, fmt.Errorf("udp-v4 punch: %w", err)
	}
	if len(info.Conns) == 0 {
		return nil, nil, errors.New("udp-v4: no conn returned")
	}
	rawConn := info.Conns[0]
	udpConn := unwrapUDP(rawConn)
	if udpConn == nil {
		return nil, nil, errors.New("udp-v4: punched conn is not UDP")
	}
	transport.ClearUDPDeadlines(udpConn)
	transport.TuneUDPBuffers(udpConn)

	isClient := info.IsClient
	mux, err := secureUpgrade(ctx, udpConn, &info.SharedKey, isClient, true, logWriter(deps))
	if err != nil {
		udpConn.Close()
		return nil, nil, fmt.Errorf("udp-v4 secure: %w", err)
	}
	return &session.Outcome{
		Mux:      mux,
		IsClient: isClient,
		Local:    udpConn.LocalAddr(),
		Remote:   rawConn.RemoteAddr(),
	}, &info.SharedKey, nil
}
