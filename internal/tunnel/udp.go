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

// claimUDPSession 持锁把新建会话回插 sessions[key]（REL-03 的 CAS 语义）：
// 同 key 已有并发创建者时，新会话是败者——不落 map，返回既有会话，
// 由调用方负责关闭败者资源（cancel/读协程退出/配额释放）
func claimUDPSession(mu *sync.Mutex, sessions map[string]*udpSession, key string, s *udpSession) (winner *udpSession, inserted bool) {
	mu.Lock()
	defer mu.Unlock()
	if existing, ok := sessions[key]; ok && existing != s {
		return existing, false
	}
	sessions[key] = s
	return s, true
}

// udpSession tracks a UDP source address session
type udpSession struct {
	srcAddr   *net.UDPAddr
	stream    net.Conn
	lastSeen  time.Time
	cancel    context.CancelFunc
	sKey      string // stats composite key (nodeID/tunnelName)
	nodeID    string // nodeID for connection limiter release
	gen       uint64 // generation token for ReleaseConn validation
	destroyed bool   // 幂等销毁标记（mu 保护）：流死亡/过期/隧道停止只回收一次
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

		// destroySession 销毁单个 UDP 会话（幂等）：节点侧流死亡也必须触发
		// 回收，否则死会话以黑洞状态占用连接配额直到过期清理
		destroySession := func(key string, s *udpSession) {
			mu.Lock()
			if s.destroyed {
				mu.Unlock()
				return
			}
			s.destroyed = true
			if cur, ok := sessions[key]; ok && cur == s {
				delete(sessions, key)
			}
			mu.Unlock()
			s.cancel()
			s.stream.Close()
			tg.stats.ConnClosed(s.sKey)
			tg.limiter.ReleaseConn(s.nodeID, s.sKey, s.gen)
		}

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
							mu.Unlock()
							destroySession(key, s)
							slog.Debug("UDP session expired", "tunnel", tunnel.Name, "src", key)
							mu.Lock()
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
				ok, gen := tg.limiter.AcquireConn(node.ID, newSKey)
				if !ok {
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
					gen:      gen,
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
							// 节点侧流死亡：销毁会话回收配额，否则黑洞到过期清理
							destroySession(key, sess)
							return
						}
						if bwLimiter := tg.limiter.BWLimiterFor(newSKey); bwLimiter != nil {
							if err := waitBurst(respCtx, bwLimiter, rn); err != nil {
								return
							}
						}
						conn.WriteToUDP(respBuf[:rn], addr)
						tg.stats.RecordBytesOut(newSKey, int64(rn))
					}
				}()

				// REL-03：回插前持锁检查同 key 是否已有并发创建的会话。
				// 败者（本会话）被销毁回收资源（读协程经 cancel 退出、流关闭、
				// 配额释放），本包改经既有会话转发
				if winner, inserted := claimUDPSession(&mu, sessions, key, sess); inserted {
					fwdStream = sess.stream
					curSKey = newSKey
				} else {
					destroySession(key, sess)
					slog.Debug("UDP session lost insert race, using existing", "tunnel", tunnel.Name, "src", key)
					winner.lastSeen = time.Now()
					fwdStream = winner.stream
					curSKey = winner.sKey
				}

				slog.Debug("UDP session created", "tunnel", tunnel.Name, "src", key)
			} else {
				sess.lastSeen = time.Now()
				fwdStream = sess.stream
				curSKey = sess.sKey
				mu.Unlock()
			}

			// 在锁外转发数据（使用锁内捕获的 stream 引用，避免竞态）
			if bwLimiter := tg.limiter.BWLimiterFor(curSKey); bwLimiter != nil {
				if err := waitBurst(tunnelCtx, bwLimiter, n); err != nil {
					continue
				}
			}
			if fwdStream != nil {
				if _, err := fwdStream.Write(buf[:n]); err != nil {
					slog.Debug("UDP write to stream failed", "tunnel", tunnel.Name, "error", err)
					// 流写死（节点侧异常）：销毁会话回收配额，后续包走新建会话路径
					if sess != nil {
						destroySession(key, sess)
					}
					continue
				}
			}
			tg.stats.RecordBytesIn(curSKey, int64(n))
		}

		// 清理所有会话
		mu.Lock()
		for _, s := range sessions {
			if s.destroyed {
				continue // 已被 destroySession 回收，避免二次释放
			}
			s.destroyed = true
			s.cancel()
			s.stream.Close()
			tg.stats.ConnClosed(s.sKey)
			tg.limiter.ReleaseConn(s.nodeID, s.sKey, s.gen)
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
