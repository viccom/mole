package node

import "moleAgent_Serv/internal/core"

// NewManager creates a sharded node manager with default shard count.
// Deprecated: Use NewShardedNodeManager directly.
func NewManager() core.NodeManager {
	return NewShardedNodeManager(defaultShardCount)
}
