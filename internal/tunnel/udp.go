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

// udpSession tracks a UDP source address session
type udpSession struct {
	srcAddr  *net.UDPAddr
	stream   net.Conn
	lastSeen time.Time
	cancel   context.CancelFunc
	sKey     string // stats composite key (nodeID/tunnelName)
	nodeID   string // nodeID for connection limiter release
}

// StartUDP 启动 UDP 隧道监听（异步，与 StartTCP 行为一致）
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

	// 隧道级 context：StopTunnel 时 cancel 让运行循环退出
	tunnelCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	tg.registry.Register(tunnel.Name, &udpListenerAdapter{conn: conn, ctx: tunnelCtx})
	tg.registry.RegisterRuntime(tunnel.Name, &tunnelRuntime{cancel: cancel, done: done})
	slog.Info("UDP tunnel listening", "tunnel", tunnel.Name, "port", tunnel.ListenPort)

	// 所有运行逻辑在 goroutine 中，StartUDP 立即返回
	go func() {
		defer close(done)
		defer tg.registry.Unregister(tunnel.Name)
		defer cancel()
		defer conn.Close()

		sessions := make(map[string]*udpSession)
		var mu sync.Mutex

		// 会话清理协程
		cleanupDone := make(chan struct{})
		go func() {
			defer close(cleanupDone)
			ticker := time.NewTicker(15 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-tunnelCtx.Done():
					return
				case <-ticker.C:
					mu.Lock()
					now := time.Now()
					for key, s := range sessions {
						if now.Sub(s.lastSeen) > udpSessionTimeout {
							s.cancel()
							s.stream.Close()
							tg.stats.ConnClosed(s.sKey)
						tg.limiter.ReleaseConn(s.nodeID, s.sKey)
							delete(sessions, key)
							slog.Debug("UDP session expired", "tunnel", tunnel.Name, "src", key)
						}
					}
					mu.Unlock()
				}
			}
		}()

		buf := make([]byte, 65535)
		for {
			n, addr, err := conn.ReadFromUDP(buf)
			if err != nil {
				// 隧道级取消或全局取消或连接已关闭 → 优雅退出
				if tunnelCtx.Err() != nil || ctx.Err() != nil || isClosedConnError(err) {
					break
				}
				slog.Error("UDP read error", "tunnel", tunnel.Name, "error", err)
				continue
			}


			key := addr.String()
			var fwdStream net.Conn // 锁内捕获，锁外安全使用
			var curSKey string     // 当前包对应的统计复合键

			mu.Lock()
			sess, ok := sessions[key]
			if !ok {
				mu.Unlock()

				// 在锁外执行耗时操作：查找节点 + 打开 stream
				node := tg.findNodeForTunnel(tunnelCtx, tunnel.Name)
				if node == nil {
					slog.Debug("No node for UDP tunnel", "tunnel", tunnel.Name)
					continue
				}

				// 检查隧道是否仍启用（索引可能过时）
				tunnelEnabled := false
				for _, t := range node.Tunnels {
					if t.Name == tunnel.Name && t.IsEnabled() {
						tunnelEnabled = true
						break
					}
				}
				if !tunnelEnabled {
					slog.Debug("UDP tunnel disabled", "tunnel", tunnel.Name)
					continue
				}

				session, err := tg.nodeMgr.GetSession(tunnelCtx, node.ID)
				if err != nil {
					slog.Error("Failed to get session for UDP", "tunnel", tunnel.Name, "nodeId", node.ID, "error", err)
					continue
				}
				newStream, err := session.OpenStream()
				if err != nil {
					slog.Error("Failed to open smux stream for UDP", "tunnel", tunnel.Name, "error", err)
					continue
				}

				// 发送隧道标识头：\x00<tunnel-name>\n，客户端据此路由到正确目标
				if _, err := newStream.Write(append([]byte{0x00}, tunnel.Name...)); err != nil {
					slog.Error("Failed to send UDP proxy header", "tunnel", tunnel.Name, "error", err)
					newStream.Close()
					continue
				}
				if _, err := newStream.Write([]byte{'\n'}); err != nil {
					slog.Error("Failed to send UDP proxy header newline", "tunnel", tunnel.Name, "error", err)
					newStream.Close()
					continue
				}

				// Connection limiting
				newSKey := statsKey(node.ID, tunnel.Name)
				if !tg.limiter.AcquireConn(node.ID, newSKey) {
					slog.Warn("UDP connection limit exceeded", "tunnel", tunnel.Name)
					newStream.Close()
					continue
				}

				// Track new UDP session as a connection
				tg.stats.ConnOpened(newSKey)

				respCtx, respCancel := context.WithCancel(tunnelCtx)
				sess = &udpSession{
					srcAddr:  addr,
					stream:   newStream,
					lastSeen: time.Now(),
					cancel:   respCancel,
					sKey:     newSKey,
					nodeID:   node.ID,
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
						newStream.SetReadDeadline(time.Now().Add(30 * time.Second))
						rn, err := newStream.Read(respBuf)
						if err != nil {
							if respCtx.Err() != nil {
								return // context 已取消，正常退出
							}
							if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
								continue
							}
							return
						}
						// 统计出站字节数
						tg.stats.RecordBytesOut(newSKey, int64(rn))
						if bwLimiter := tg.limiter.BWLimiterFor(newSKey); bwLimiter != nil {
							bwLimiter.WaitN(respCtx, rn)
						}
						conn.WriteToUDP(respBuf[:rn], addr)
					}
				}()

				mu.Lock()
				sessions[key] = sess
				fwdStream = sess.stream
				curSKey = newSKey
				mu.Unlock()

				slog.Debug("UDP session created", "tunnel", tunnel.Name, "src", key)
			} else {
				sess.lastSeen = time.Now()
				fwdStream = sess.stream
				curSKey = sess.sKey
				mu.Unlock()
			}

			// 在锁外转发数据（使用锁内捕获的 stream 引用，避免竞态）
			// 统计入站字节数（使用复合键）
			tg.stats.RecordBytesIn(curSKey, int64(n))
				if bwLimiter := tg.limiter.BWLimiterFor(curSKey); bwLimiter != nil {
					bwLimiter.WaitN(tunnelCtx, n)
				}
			if fwdStream != nil {
				if _, err := fwdStream.Write(buf[:n]); err != nil {
					slog.Debug("UDP write to stream failed", "tunnel", tunnel.Name, "error", err)
				}
			}
		}

		// 清理所有会话
		mu.Lock()
		for _, s := range sessions {
			s.cancel()
			s.stream.Close()
			tg.stats.ConnClosed(s.sKey)
				tg.limiter.ReleaseConn(s.nodeID, s.sKey)
		}
		sessions = make(map[string]*udpSession)
		mu.Unlock()

		// 等待清理协程退出
		<-cleanupDone

		slog.Info("UDP tunnel stopped", "tunnel", tunnel.Name)
	}()

	return nil
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
