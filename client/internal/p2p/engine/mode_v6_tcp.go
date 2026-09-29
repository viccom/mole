//go:build p2p

package engine

import (
	"bytes"
	"context"
	"encoding/base64"
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

// ModeV6TCP: IPv6 direct connection over TCP6 (no NAT, no STUN, no hole-punch)
// + secure(TLS) + yamux。最可靠的 IPv6 路径——plain TCP6 端到端验证可达。
//
// 双向 probe：BOTH peers listen+accept AND dial，first successful wins。IPv6 路由
// 常非对称（A→B 通但 B→A 不通）。单向角色在阻塞方向必败，双向覆盖两者。非对称时
// 只一个方向成功，两端 race 各自落到同一（唯一）连接——天然配对。
func ModeV6TCP(ctx context.Context, deps Deps) (*session.Outcome, *[crypto.KeyLen]byte, error) {
	guas := discoverGlobalIPv6Addrs()
	if len(guas) == 0 {
		return nil, nil, errors.New("v6-tcp: no global unicast IPv6 address")
	}

	ln, err := net.Listen("tcp6", "[::]:0")
	if err != nil {
		return nil, nil, fmt.Errorf("v6-tcp listen: %w", err)
	}
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		ln.Close()
		return nil, nil, errors.New("v6-tcp: listener addr not TCP6")
	}
	port := tcpAddr.Port
	localAddrs := make([]string, 0, len(guas))
	for _, gua := range guas {
		localAddrs = append(localAddrs, net.JoinHostPort(gua, fmt.Sprint(port)))
	}

	myKey, err := crypto.GenerateECDHEKey()
	if err != nil {
		ln.Close()
		return nil, nil, fmt.Errorf("v6-tcp ecdhe: %w", err)
	}
	local := &easyp2p.IPv6DirectPayload{
		Addrs:  localAddrs,
		PubKey: base64.StdEncoding.EncodeToString(myKey.PublicKeyBytes()),
	}
	remote, err := easyp2p.ExchangeIPv6Address(ctx, deps.Room, local, 60*time.Second, logWriter(deps))
	if err != nil {
		ln.Close()
		return nil, nil, err
	}
	peerPubBytes, err := base64.StdEncoding.DecodeString(remote.PubKey)
	if err != nil {
		ln.Close()
		return nil, nil, fmt.Errorf("v6-tcp peer pubkey decode: %w", err)
	}
	peerPub, err := crypto.FromPublicKeyBytes(peerPubBytes)
	if err != nil {
		ln.Close()
		return nil, nil, fmt.Errorf("v6-tcp peer pubkey parse: %w", err)
	}
	sharedKey, err := myKey.DeriveSharedKey(peerPub)
	if err != nil {
		ln.Close()
		return nil, nil, fmt.Errorf("v6-tcp ecdh: %w", err)
	}

	peerAddrs := make([]*net.TCPAddr, 0, len(remote.Addrs))
	for _, a := range remote.Addrs {
		if ta, e := net.ResolveTCPAddr("tcp6", a); e == nil {
			peerAddrs = append(peerAddrs, ta)
		}
	}
	if len(peerAddrs) == 0 {
		ln.Close()
		return nil, nil, errors.New("v6-tcp: no valid peer IPv6 address")
	}

	// 角色在探测前确定，供双向探测配对使用（两端结果必须互补）：
	// 地址序比较为主；地址集合完全相同（同机多实例互发现）时 `<` 两端同为
	// false 不再互补，改用双方必然不同的 ECDHE 公钥序做非对称打破
	isClient := strings.Join(sortedCopy(localAddrs), ",") < strings.Join(sortedCopy(remote.Addrs), ",")
	if strings.Join(sortedCopy(localAddrs), ",") == strings.Join(sortedCopy(remote.Addrs), ",") {
		isClient = bytes.Compare(myKey.PublicKeyBytes(), peerPubBytes) < 0
	}

	tcpConn, _, err := tcpProbeV6Bidirectional(ctx, ln, peerAddrs, 15*time.Second, isClient)
	if err != nil {
		return nil, nil, fmt.Errorf("v6-tcp connect: %w", err)
	}
	transport.TuneTCPConn(tcpConn)

	mux, err := secureUpgrade(ctx, tcpConn, sharedKey, isClient, false, logWriter(deps))
	if err != nil {
		tcpConn.Close()
		return nil, nil, fmt.Errorf("v6-tcp secure: %w", err)
	}
	return &session.Outcome{
		Mux:      mux,
		IsClient: isClient,
		Local:    tcpConn.LocalAddr(),
		Remote:   tcpConn.RemoteAddr(),
	}, sharedKey, nil
}

// tcpProbeV6Bidirectional runs accept (peer→me) and dial (me→peer) concurrently.
// 非对称路由下只有单方向成功，天然配对；对称路由下两端可能各自持有不同物理连接，
// first-result-wins 会配对错位（各自关掉对方保留连接的对端），
// 因此成功方向齐全时必须按两端互补的确定性角色配对：
// preferDial 一端保留自己拨出的连接，对端保留自己接受的连接——同一条物理连接。
func tcpProbeV6Bidirectional(ctx context.Context, ln net.Listener, peerAddrs []*net.TCPAddr, timeout time.Duration, preferDial bool) (*net.TCPConn, string, error) {
	type res struct {
		conn *net.TCPConn
		via  string
	}
	ch := make(chan res, 2)

	go func() {
		c, err := tcpAcceptV6(ctx, ln, timeout)
		if err != nil {
			ch <- res{nil, "accept: " + err.Error()}
			return
		}
		ch <- res{c, "accept (peer→me)"}
	}()
	go func() {
		c, err := tcpDialRaceV6(ctx, peerAddrs, timeout)
		if err != nil {
			ch <- res{nil, "dial: " + err.Error()}
			return
		}
		ch <- res{c, "dial (me→peer)"}
	}()

	var dialRes, acceptRes *res
	var errs []string
	for i := 0; i < 2; i++ {
		r := <-ch
		if r.conn != nil {
			switch {
			case strings.HasPrefix(r.via, "dial"):
				dialRes = &r
			default:
				acceptRes = &r
			}
		} else {
			errs = append(errs, r.via)
		}
	}
	switch {
	case dialRes != nil && acceptRes != nil:
		if preferDial {
			acceptRes.conn.Close() // loser
			return dialRes.conn, dialRes.via, nil
		}
		dialRes.conn.Close() // loser
		return acceptRes.conn, acceptRes.via, nil
	case dialRes != nil:
		return dialRes.conn, dialRes.via, nil
	case acceptRes != nil:
		return acceptRes.conn, acceptRes.via, nil
	}
	return nil, "", fmt.Errorf("both directions failed: %s", strings.Join(errs, "; "))
}

// tcpAcceptV6 accepts one incoming TCP6 connection within timeout.
func tcpAcceptV6(ctx context.Context, ln net.Listener, timeout time.Duration) (*net.TCPConn, error) {
	done := make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept()
		done <- c
	}()
	select {
	case c := <-done:
		ln.Close()
		if c == nil {
			return nil, errors.New("accept failed")
		}
		return c.(*net.TCPConn), nil
	case <-ctx.Done():
		ln.Close()
		return nil, ctx.Err()
	case <-time.After(timeout):
		ln.Close()
		return nil, errors.New("accept timeout (peer did not dial)")
	}
}

// tcpDialRaceV6 concurrently dials each peer TCP6 address, returns the first
// successful connection (closing losers). Handles multi-GUA peers.
func tcpDialRaceV6(ctx context.Context, peerAddrs []*net.TCPAddr, timeout time.Duration) (*net.TCPConn, error) {
	if len(peerAddrs) == 0 {
		return nil, errors.New("no peer addrs")
	}
	type res struct {
		conn *net.TCPConn
		err  error
	}
	ch := make(chan res, len(peerAddrs))
	dialer := &net.Dialer{Timeout: timeout}
	for _, peer := range peerAddrs {
		go func(p *net.TCPAddr) {
			c, e := dialer.DialContext(ctx, "tcp6", p.String())
			if e == nil {
				tc, ok := c.(*net.TCPConn)
				if !ok {
					c.Close()
					ch <- res{nil, errors.New("v6-tcp: dialed conn not TCP6")}
				} else {
					ch <- res{tc, nil}
				}
			} else {
				ch <- res{nil, e}
			}
		}(peer)
	}
	var firstConn *net.TCPConn
	var errs []string
	for i := 0; i < len(peerAddrs); i++ {
		r := <-ch
		if r.err == nil {
			if firstConn == nil {
				firstConn = r.conn
			} else {
				r.conn.Close()
			}
		} else {
			errs = append(errs, r.err.Error())
		}
	}
	if firstConn != nil {
		return firstConn, nil
	}
	return nil, fmt.Errorf("all dials failed: %s", strings.Join(errs, "; "))
}
