package node

import (
	"context"
	"sync"

	"moleAgent_Serv/internal/core"
)

const defaultShardCount = 256

// NodeShard 节点分片
type NodeShard struct {
	mu      sync.RWMutex
	clients map[string]*core.Node
}

// ShardedNodeManager 分片节点管理器
type ShardedNodeManager struct {
	shards     []*NodeShard
	shardCount int
}

// NewShardedNodeManager 创建分片节点管理器
func NewShardedNodeManager(shardCount int) *ShardedNodeManager {
	if shardCount <= 0 {
		shardCount = defaultShardCount
	}
	mgr := &ShardedNodeManager{
		shards:     make([]*NodeShard, shardCount),
		shardCount: shardCount,
	}
	for i := 0; i < shardCount; i++ {
		mgr.shards[i] = &NodeShard{
			clients: make(map[string]*core.Node),
		}
	}
	return mgr
}

// getShard 根据 nodeID 哈希获取分片
func (m *ShardedNodeManager) getShard(nodeID string) *NodeShard {
	hash := 0
	for _, c := range nodeID {
		hash = hash*31 + int(c)
	}
	if hash < 0 {
		hash = -hash
	}
	return m.shards[hash%m.shardCount]
}

// Add 添加节点
func (m *ShardedNodeManager) Add(_ context.Context, node *core.Node) error {
	shard := m.getShard(node.ID)
	shard.mu.Lock()
	if _, exists := shard.clients[node.ID]; exists {
		shard.mu.Unlock()
		return core.ErrNodeExists
	}
	shard.clients[node.ID] = node
	shard.mu.Unlock()
	return nil
}

// Remove 删除节点
func (m *ShardedNodeManager) Remove(_ context.Context, nodeID string) error {
	shard := m.getShard(nodeID)
	shard.mu.Lock()
	delete(shard.clients, nodeID)
	shard.mu.Unlock()
	return nil
}

// Get 获取节点
func (m *ShardedNodeManager) Get(_ context.Context, nodeID string) (*core.Node, bool) {
	shard := m.getShard(nodeID)
	shard.mu.RLock()
	node, ok := shard.clients[nodeID]
	shard.mu.RUnlock()
	return node, ok
}

// GetAll 返回所有节点快照（逐分片加锁读取）
func (m *ShardedNodeManager) GetAll(_ context.Context) []*core.Node {
	var result []*core.Node
	for _, shard := range m.shards {
		shard.mu.RLock()
		for _, n := range shard.clients {
			result = append(result, n)
		}
		shard.mu.RUnlock()
	}
	return result
}

// Disconnect 断开节点连接
func (m *ShardedNodeManager) Disconnect(_ context.Context, nodeID string) error {
	shard := m.getShard(nodeID)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	node, ok := shard.clients[nodeID]
	if !ok {
		return core.ErrNodeNotFound
	}
	if node.Session != nil {
		node.Session.Close()
	}
	node.Status = core.NodeStatusOffline
	delete(shard.clients, nodeID)
	return nil
}
