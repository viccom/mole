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
	cancel   context.CancelFunc // 用于取消响应读取 goroutine
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
						s.cancel()
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
			s.cancel()
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
			mu.Unlock()

			// 在锁外执行耗时操作：查找节点 + 打开 stream
			node := tg.findNodeForTunnel(ctx, tunnel.Name)
			if node == nil {
				slog.Debug("No node for UDP tunnel", "tunnel", tunnel.Name)
				continue
			}
			session, err := tg.nodeMgr.GetSession(ctx, node.ID)
				if err != nil {
					slog.Error("Failed to get session for UDP", "tunnel", tunnel.Name, "nodeId", node.ID, "error", err)
					continue
				}
				stream, err := session.OpenStream()
				if err != nil {
					slog.Error("Failed to open smux stream for UDP", "tunnel", tunnel.Name, "error", err)
				continue
			}

			// 为响应 goroutine 创建独立 context
			respCtx, respCancel := context.WithCancel(ctx)
			sess = &udpSession{
				srcAddr:  addr,
				stream:   stream,
				lastSeen: time.Now(),
				cancel:   respCancel,
			}

			// 读取响应协程（带 context 取消支持）
			go func() {
				respBuf := make([]byte, 65535)
				for {
					select {
					case <-respCtx.Done():
						return
					default:
					}
					// 设置读取超时，避免永久阻塞
					stream.SetReadDeadline(time.Now().Add(30 * time.Second))
					rn, err := stream.Read(respBuf)
					if err != nil {
						if respCtx.Err() != nil {
							return // context 已取消，正常退出
						}
						// 超时或读取错误，检查是否应该重试
						if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
							continue
						}
						return
					}
					conn.WriteToUDP(respBuf[:rn], addr)
				}
			}()

			mu.Lock()
			sessions[key] = sess
			mu.Unlock()

			slog.Debug("UDP session created", "tunnel", tunnel.Name, "src", key)
		} else {
			sess.lastSeen = time.Now()
			mu.Unlock()
		}

		// 在锁外转发数据
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
