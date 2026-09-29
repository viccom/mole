//go:build p2p

package engine

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"moleAgent_client/internal/p2p/crypto"
	"moleAgent_client/internal/p2p/easyp2p"
	"moleAgent_client/internal/p2p/netutil"
	"moleAgent_client/internal/p2p/session"
	"moleAgent_client/internal/p2p/transport"
)

// ModeV6UDP: IPv6 UDP6 direct + secure(DTLS+KCP) + yamux。
//
// v6 无 NAT，两端 mutually reachable（若路由通）。对称单连：两端都 DialUDP 到对端
// 字典序最小 GUA、源用本地字典序最小 GUA，形成对称 connected 对，DTLS client/server
// 在其上握手。非对称路由场景由 tcp-v6（双向 TCP probe，默认链第二位）兜底。
func ModeV6UDP(ctx context.Context, deps Deps) (*session.Outcome, *[crypto.KeyLen]byte, error) {
	guas := discoverGlobalIPv6Addrs()
	if len(guas) == 0 {
		return nil, nil, errors.New("udp-v6: no global unicast IPv6 address")
	}

	// 本地端口仅用于交换地址（ExchangeIPv6Address 走 MQTT，不依赖此 socket）。
	tmp, err := net.ListenUDP("udp6", &net.UDPAddr{Port: deps.ListenPort})
	if err != nil {
		return nil, nil, fmt.Errorf("udp-v6 udp6 listen: %w", err)
	}
	udpAddr, ok := tmp.LocalAddr().(*net.UDPAddr)
	if !ok {
		tmp.Close()
		return nil, nil, errors.New("udp-v6: listener addr not UDP6")
	}
	port := udpAddr.Port
	localAddrs := make([]string, 0, len(guas))
	for _, gua := range guas {
		localAddrs = append(localAddrs, net.JoinHostPort(gua, fmt.Sprint(port)))
	}
	tmp.Close()

	// ECDHE（secure PSK 来源）
	myKey, err := crypto.GenerateECDHEKey()
	if err != nil {
		return nil, nil, fmt.Errorf("udp-v6 ecdhe: %w", err)
	}
	local := &easyp2p.IPv6DirectPayload{
		Addrs:  localAddrs,
		PubKey: base64.StdEncoding.EncodeToString(myKey.PublicKeyBytes()),
	}
	remote, err := easyp2p.ExchangeIPv6Address(ctx, deps.Room, local, 60*time.Second, logWriter(deps))
	if err != nil {
		return nil, nil, err
	}
	peerPubBytes, err := base64.StdEncoding.DecodeString(remote.PubKey)
	if err != nil {
		return nil, nil, fmt.Errorf("udp-v6 peer pubkey decode: %w", err)
	}
	peerPub, err := crypto.FromPublicKeyBytes(peerPubBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("udp-v6 peer pubkey parse: %w", err)
	}
	sharedKey, err := myKey.DeriveSharedKey(peerPub)
	if err != nil {
		return nil, nil, fmt.Errorf("udp-v6 ecdh: %w", err)
	}

	// 对称单连：两端都选字典序最小 GUA，保证 connected 对对称（源/目的两端一致）。
	localMinGUA := sortedCopy(guas)[0]
	localAddr, err := net.ResolveUDPAddr("udp6", net.JoinHostPort(localMinGUA, fmt.Sprint(port)))
	if err != nil {
		return nil, nil, fmt.Errorf("udp-v6 resolve local: %w", err)
	}
	sortedPeer := sortedCopy(remote.Addrs)
	if len(sortedPeer) == 0 {
		return nil, nil, errors.New("udp-v6: no peer IPv6 address")
	}
	peerMinAddr, err := net.ResolveUDPAddr("udp6", sortedPeer[0])
	if err != nil {
		return nil, nil, fmt.Errorf("udp-v6 resolve peer: %w", err)
	}

	// H6：DialContext 响应 ctx 取消（原 net.DialUDP 无 ctx，Disconnect 时要等 secureUpgrade 超时才退）
	dialer := &net.Dialer{LocalAddr: localAddr}
	c, err := dialer.DialContext(ctx, "udp6", peerMinAddr.String())
	if err != nil {
		return nil, nil, fmt.Errorf("udp-v6 dial: %w", err)
	}
	connected := c.(*net.UDPConn)
	transport.TuneUDPBuffers(connected)

	isClient := strings.Join(sortedCopy(localAddrs), ",") < strings.Join(sortedCopy(remote.Addrs), ",")
	mux, err := secureUpgrade(ctx, connected, sharedKey, isClient, true, logWriter(deps))
	if err != nil {
		connected.Close()
		return nil, nil, fmt.Errorf("udp-v6 secure: %w", err)
	}
	return &session.Outcome{
		Mux:      mux,
		IsClient: isClient,
		Local:    connected.LocalAddr(),
		Remote:   connected.RemoteAddr(),
	}, sharedKey, nil
}

// discoverGlobalIPv6Addrs 返回本机全局单播 IPv6（排除 ULA fc00::/7，IsGlobalUnicast
// 对 ULA 也返回 true，需 !IsPrivate 过滤）。
func discoverGlobalIPv6Addrs() []string {
	var out []string
	for _, la := range netutil.DiscoverAllLocalAddrs() {
		if la.IsIPv6 && la.IP.IsGlobalUnicast() && !la.IP.IsPrivate() {
			out = append(out, la.IP.String())
		}
	}
	return out
}

// sortedCopy returns a sorted copy of s (non-mutating).
func sortedCopy(s []string) []string {
	cp := append([]string(nil), s...)
	sort.Strings(cp)
	return cp
}
