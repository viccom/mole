package node

import (
	"context"
	"log/slog"
	"time"

	"github.com/xtaci/smux"

	"moleAgent_Serv/internal/core"
)

const (
	healthCheckInterval = 30 * time.Second
	heartbeatTimeout    = 90 * time.Second
)

// StartHealthCheck 启动健康检查协程，ctx 取消时退出
// onDisconnect 在节点被清理后调用，用于释放隧道运行时资源
func StartHealthCheck(ctx context.Context, mgr *ShardedNodeManager, onDisconnect func(nodeID string, tunnels []core.Tunnel)) {
	ticker := time.NewTicker(healthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("Health check stopped")
			return
		case <-ticker.C:
			cleanupDeadNodes(ctx, mgr, onDisconnect)
		}
	}
}

func cleanupDeadNodes(ctx context.Context, mgr *ShardedNodeManager, onDisconnect func(nodeID string, tunnels []core.Tunnel)) {
	now := time.Now()

	// 按分片独立收集和清理，避免跨分片不一致
	for _, shard := range mgr.shards {
		type deadNodeInfo struct {
			id      string
			tunnels []core.Tunnel
			sess    *smux.Session
		}
		var deadNodes []deadNodeInfo

		shard.mu.RLock()
		for id, node := range shard.clients {
			// 仅清理"在线但心跳超时"的节点，不误删预配置离线节点
			if node.Status == core.NodeStatusOnline &&
				node.LastHeartbeat != nil &&
				now.Sub(*node.LastHeartbeat) > heartbeatTimeout {
				dn := deadNodeInfo{id: id, tunnels: append([]core.Tunnel(nil), node.Tunnels...)}
				if sess, ok := shard.sessions[id]; ok {
					dn.sess = sess
				}
				deadNodes = append(deadNodes, dn)
			}
		}
		shard.mu.RUnlock()

		for _, dn := range deadNodes {
			// 清理前复查会话代际：快照与 Disconnect 之间节点可能已完成重连置换
			//（probeOldSession → Remove → Add → AddSession），当前会话已是新会话。
			// 不加复查会把刚注册成功的新节点连同新会话一起清掉，客户端自认在线
			// 而路由全断。会话缺失（临时无表项）维持原语义继续清理
			if cur, err := mgr.GetSession(ctx, dn.id); err == nil {
				// GetSession 返回 any（core.NodeSessionProvider，架构审查 🔴5c）：
				// 必须断言后比较，否则 any(*smux.Session) 与 *smux.Session 类型不等，
				// 会把已置换的新会话误判为「未置换」而错误清理健康节点。
				// 断言失败（类型异常）保守跳过清理，宁可漏清不可误清
				s, ok := cur.(*smux.Session)
				if !ok || s != dn.sess {
					slog.Debug("Skip dead node cleanup: session replaced", "nodeId", dn.id)
					continue
				}
			}
			if err := mgr.Disconnect(ctx, dn.id); err != nil {
				slog.Warn("Failed to disconnect dead node", "nodeId", dn.id, "error", err)
			} else {
				slog.Warn("Cleaned up dead node", "nodeId", dn.id, "reason", "heartbeat timeout")
			}
			if onDisconnect != nil && len(dn.tunnels) > 0 {
				onDisconnect(dn.id, dn.tunnels)
			}
		}
	}
}
