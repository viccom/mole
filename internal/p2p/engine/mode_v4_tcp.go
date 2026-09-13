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

// ModeV4TCP: IPv4 TCP hole punch (port offset +100, 600-port birthday paradox,
// 800 workers, SO_REUSEADDR simultaneous open) + secure(TLS) + yamux。
func ModeV4TCP(ctx context.Context, deps Deps) (*session.Outcome, *[crypto.KeyLen]byte, error) {
	info, err := easyp2p.Easy_P2P_MP(ctx, "tcp4", "", deps.Room, false, nil, logWriter(deps), nil)
	if err != nil {
		return nil, nil, fmt.Errorf("v4-tcp punch: %w", err)
	}
	if len(info.Conns) == 0 {
		return nil, nil, errors.New("v4-tcp: no conn returned")
	}
	tcpConn := unwrapTCP(info.Conns[0])
	if tcpConn == nil {
		return nil, nil, errors.New("v4-tcp: punched conn is not TCP")
	}
	transport.TuneTCPConn(tcpConn)

	isClient := info.IsClient
	mux, err := secureUpgrade(ctx, tcpConn, &info.SharedKey, isClient, false, logWriter(deps))
	if err != nil {
		tcpConn.Close()
		return nil, nil, fmt.Errorf("v4-tcp secure: %w", err)
	}
	return &session.Outcome{
		Mux:      mux,
		IsClient: isClient,
		Local:    tcpConn.LocalAddr(),
		Remote:   tcpConn.RemoteAddr(),
	}, &info.SharedKey, nil
}
