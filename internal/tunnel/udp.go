package tunnel

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"moleAgent_Serv/internal/core"
)

const udpSessionTimeout = 60 * time.Second

// udpSession 跟踪一个 UDP 源地址的会话
type udpSession struct {
	srcAddr  *net.UDPAddr
	stream   net.Conn
	lastSeen time.Time
}

// StartUDP 启动 UDP 隧道监听
func (tg *TunnelGateway) StartUDP(ctx context.Context, tunnel core.Tunnel) error {
	listenAddr := fmt.Sprintf(":%d", tunnel.ListenPort)
	udpAddr, err := net.ResolveUDPAddr("udp", listenAddr)
	if err != nil {
		return fmt.Errorf("resolve udp %s: %w", listenAddr, err)
	}

	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return fmt.Errorf("udp listen %s: %w", listenAddr, err)
	}

	slog.Info("UDP tunnel listening", "tunnel", tunnel.Name, "port", tunnel.ListenPort)

	sessions := make(map[string]*udpSession)
	var mu sync.Mutex

	// 会话清理协程
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				mu.Lock()
				now := time.Now()
				for key, s := range sessions {
					if now.Sub(s.lastSeen) > udpSessionTimeout {
						s.stream.Close()
						delete(sessions, key)
						slog.Debug("UDP session expired", "tunnel", tunnel.Name, "src", key)
					}
				}
				mu.Unlock()
			}
		}
	}()

	go func() {
		<-ctx.Done()
		conn.Close()
		mu.Lock()
		for _, s := range sessions {
			s.stream.Close()
		}
		sessions = make(map[string]*udpSession)
		mu.Unlock()
		tg.registry.Unregister(tunnel.Name)
		slog.Info("UDP tunnel stopped", "tunnel", tunnel.Name)
	}()

	tg.registry.Register(tunnel.Name, &udpListenerAdapter{conn: conn, ctx: ctx})

	buf := make([]byte, 65535)
	for {
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				slog.Error("UDP read error", "tunnel", tunnel.Name, "error", err)
				continue
			}
		}

		key := addr.String()
		mu.Lock()
		sess, ok := sessions[key]
		if !ok {
			// 新会话
			node := tg.findNodeForTunnel(ctx, tunnel.Name)
			if node == nil {
				mu.Unlock()
				slog.Debug("No node for UDP tunnel", "tunnel", tunnel.Name)
				continue
			}
			stream, err := node.Session.OpenStream()
			if err != nil {
				mu.Unlock()
				slog.Error("Failed to open smux stream for UDP", "tunnel", tunnel.Name, "error", err)
				continue
			}
			sess = &udpSession{
				srcAddr:  addr,
				stream:   stream,
				lastSeen: time.Now(),
			}
			sessions[key] = sess

			// 读取响应协程
			go func() {
				respBuf := make([]byte, 65535)
				for {
					rn, err := stream.Read(respBuf)
					if err != nil {
						return
					}
					conn.WriteToUDP(respBuf[:rn], addr)
				}
			}()

			slog.Debug("UDP session created", "tunnel", tunnel.Name, "src", key)
		}
		sess.lastSeen = time.Now()
		mu.Unlock()

		// 转发数据
		sess.stream.Write(buf[:n])
	}
}

// udpListenerAdapter 使 UDP conn 满足 net.Listener 接口（简化注册）
type udpListenerAdapter struct {
	conn *net.UDPConn
	ctx  context.Context
}

func (a *udpListenerAdapter) Accept() (net.Conn, error) {
	<-a.ctx.Done()
	return nil, fmt.Errorf("closed")
}

func (a *udpListenerAdapter) Close() error {
	return a.conn.Close()
}

func (a *udpListenerAdapter) Addr() net.Addr {
	return a.conn.LocalAddr()
}
