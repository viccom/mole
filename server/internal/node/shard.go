package node

import (
	"context"
	"fmt"
	"hash/fnv"
	"sync"
	"time"

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

// RetireNode 统一的节点退出编排（架构审查 🔴6）：把「标记离线 → 从管理器移除
// → 关闭会话」这组领域操作收敛为唯一入口，供三个触发点复用：
//   - tunnel 控制面：smux 会话结束（经会话置换判定后）
//   - node 健康检查：心跳超时（经会话代际复查后）
//   - api 管理面：管理员断开/删除节点
//
// 调用方负责各自的触发前判定（置换/代际/归属检查）与触发后副作用
// （释放监听器索引统计、吊销凭据、持久层删除）——那些是各入口的真实差异，
// 不应混进本函数。返回 false 表示节点已不存在（幂等，非错误）。
func (m *ShardedNodeManager) RetireNode(ctx context.Context, nodeID string) bool {
	if _, ok := m.Get(ctx, nodeID); !ok {
		return false
	}
	// 先置离线再移除：Remove 后节点已不可见，离线态无处落
	if err := m.Update(ctx, nodeID, func(n *core.Node) {
		n.Status = core.NodeStatusOffline
	}); err != nil {
		return false
	}
	return m.Remove(ctx, nodeID) == nil
}

// snapshotNode 返回节点快照副本：Tunnels/ClientStatuses 等切片字段由写方在
// 分片锁内整体替换，锁释放后调用方持活指针裸读字段构成数据竞争
//（API 序列化、RebuildIndex 遍历、断连清理拷贝均发生在锁外）
func snapshotNode(n *core.Node) *core.Node {
	cp := *n
	cp.Tunnels = append([]core.Tunnel(nil), n.Tunnels...)
	cp.ClientStatuses = append([]core.ClientTunnelStatus(nil), n.ClientStatuses...)
	return &cp
}

// Get 获取节点（返回快照副本；写操作请走 Update）
func (m *ShardedNodeManager) Get(_ context.Context, nodeID string) (*core.Node, bool) {
	shard := m.getShard(nodeID)
	shard.mu.RLock()
	node, ok := shard.clients[nodeID]
	shard.mu.RUnlock()
	if !ok {
		return nil, false
	}
	snap := snapshotNode(node)
	return snap, true
}

// GetAll 返回所有节点快照（逐分片加锁读取）
func (m *ShardedNodeManager) GetAll(_ context.Context) []*core.Node {
	var result []*core.Node
	for _, shard := range m.shards {
		shard.mu.RLock()
		for _, n := range shard.clients {
			result = append(result, snapshotNode(n))
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

// GetSession 在分片锁保护下获取节点的 smux Session。
// 返回 any 以满足 core.NodeSessionProvider（架构审查 🔴5c：让 api 层依赖
// core 接口而非本包具体类型）；实现仍返回 *smux.Session，调用方断言取用。
func (m *ShardedNodeManager) GetSession(_ context.Context, nodeID string) (any, error) {
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

// ProbeSession 探测节点 session 是否真正存活。
// 尝试在 5 秒超时内打开一个流：成功说明底层连接有效，失败说明连接已断。
func (m *ShardedNodeManager) ProbeSession(_ context.Context, nodeID string) error {
	shard := m.getShard(nodeID)
	shard.mu.RLock()
	sess, hasSess := shard.sessions[nodeID]
	shard.mu.RUnlock()

	if !hasSess || sess.IsClosed() {
		return core.ErrNodeOffline
	}

	type probeResult struct {
		stream *smux.Stream
		err    error
	}
	ch := make(chan probeResult, 1)
	go func() {
		s, err := sess.OpenStream()
		ch <- probeResult{s, err}
	}()

	select {
	case r := <-ch:
		if r.err != nil {
			return fmt.Errorf("probe: %w", r.err)
		}
		r.stream.Close()
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("probe: timeout")
	}
}
