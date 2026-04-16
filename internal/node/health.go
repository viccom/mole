package node

import (
	"context"
	"log/slog"
	"time"

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
		}
		var deadNodes []deadNodeInfo

		shard.mu.RLock()
		for id, node := range shard.clients {
			// 仅清理"在线但心跳超时"的节点，不误删预配置离线节点
			if node.Status == core.NodeStatusOnline &&
				node.LastHeartbeat != nil &&
				now.Sub(*node.LastHeartbeat) > heartbeatTimeout {
				deadNodes = append(deadNodes, deadNodeInfo{id: id, tunnels: append([]core.Tunnel(nil), node.Tunnels...)})
			}
		}
		shard.mu.RUnlock()

		for _, dn := range deadNodes {
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
