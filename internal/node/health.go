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
func StartHealthCheck(ctx context.Context, mgr *ShardedNodeManager) {
	ticker := time.NewTicker(healthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("Health check stopped")
			return
		case <-ticker.C:
			cleanupDeadNodes(ctx, mgr)
		}
	}
}

func cleanupDeadNodes(ctx context.Context, mgr *ShardedNodeManager) {
	now := time.Now()
	var deadIDs []string

	for _, shard := range mgr.shards {
		shard.mu.RLock()
		for id, node := range shard.clients {
			if node.Status == core.NodeStatusOffline {
				deadIDs = append(deadIDs, id)
			} else if node.LastHeartbeat != nil && now.Sub(*node.LastHeartbeat) > heartbeatTimeout {
				deadIDs = append(deadIDs, id)
			}
		}
		shard.mu.RUnlock()
	}

	for _, id := range deadIDs {
		if err := mgr.Disconnect(ctx, id); err != nil {
			slog.Warn("Failed to disconnect dead node", "nodeId", id, "error", err)
		} else {
			slog.Warn("Cleaned up dead node", "nodeId", id, "reason", "heartbeat timeout")
		}
	}
}
