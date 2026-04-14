package node

import (
	"context"
	"hash/fnv"
	"sync"

	"moleAgent_Serv/internal/core"

	"github.com/xtaci/smux"
)

const defaultShardCount = 256

// NodeShard 节点分片
type NodeShard struct {
	mu       sync.RWMutex
	clients  map[string]*core.Node
	sessions map[string]*smux.Session // 独立的运行态会话表（nodeID → session）
}

// ShardedNodeManager 分片节点管理器
// 节点与会话分离：clients=Node领域模型不含Session，sessions=独立运行态会话
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
			clients:  make(map[string]*core.Node),
			sessions: make(map[string]*smux.Session),
		}
	}
	return mgr
}

// getShard 根据 nodeID 哈希获取分片（FNV-1a）
func (m *ShardedNodeManager) getShard(nodeID string) *NodeShard {
	h := fnv.New32a()
	h.Write([]byte(nodeID))
	return m.shards[h.Sum32()%uint32(m.shardCount)]
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
	if sess, ok := shard.sessions[nodeID]; ok {
		sess.Close()
		delete(shard.sessions, nodeID)
	}
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

// Update 更新节点信息（通过回调函数原子修改）
func (m *ShardedNodeManager) Update(_ context.Context, nodeID string, fn func(*core.Node)) error {
	shard := m.getShard(nodeID)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	node, ok := shard.clients[nodeID]
	if !ok {
		return core.ErrNodeNotFound
	}
	fn(node)
	return nil
}

// Disconnect 断开节点连接
func (m *ShardedNodeManager) Disconnect(_ context.Context, nodeID string) error {
	shard := m.getShard(nodeID)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	_, ok := shard.clients[nodeID]
	if !ok {
		return core.ErrNodeNotFound
	}
	if sess, hasSess := shard.sessions[nodeID]; hasSess {
		sess.Close()
		delete(shard.sessions, nodeID)
	}
	delete(shard.clients, nodeID)
	return nil
}

// AddSession 为在线节点绑定运行态会话（独立于 Node 领域模型）
func (m *ShardedNodeManager) AddSession(_ context.Context, nodeID string, sess *smux.Session) {
	shard := m.getShard(nodeID)
	shard.mu.Lock()
	shard.sessions[nodeID] = sess
	shard.mu.Unlock()
}

// RemoveSession 移除运行态会话（不断开节点本身）
func (m *ShardedNodeManager) RemoveSession(_ context.Context, nodeID string) {
	shard := m.getShard(nodeID)
	shard.mu.Lock()
	if sess, ok := shard.sessions[nodeID]; ok {
		sess.Close()
		delete(shard.sessions, nodeID)
	}
	shard.mu.Unlock()
}

// GetSession 在分片锁保护下获取节点的 smux Session
func (m *ShardedNodeManager) GetSession(_ context.Context, nodeID string) (*smux.Session, error) {
	shard := m.getShard(nodeID)
	shard.mu.RLock()
	defer shard.mu.RUnlock()
	node, ok := shard.clients[nodeID]
	if !ok {
		return nil, core.ErrNodeNotFound
	}
	if node.Status != core.NodeStatusOnline {
		return nil, core.ErrNodeOffline
	}
	if sess, hasSess := shard.sessions[nodeID]; hasSess && !sess.IsClosed() {
		return sess, nil
	}
	return nil, core.ErrNodeOffline
}
