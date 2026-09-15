package tunnel

import (
	"sync"
	"sync/atomic"
	"time"

	"moleAgent_Serv/internal/core"
)

// statsKey 生成统计复合键，避免不同节点同名隧道串台
func statsKey(nodeID, tunnelName string) string {
	return nodeID + "/" + tunnelName
}

// statsEntry 内部统计条目（使用原子操作保证并发安全）
type statsEntry struct {
	BytesIn      int64
	BytesOut     int64
	TotalConns   int64
	ActiveConns  int64
	lastActivity int64 // Unix nanos，通过 atomic 操作
}

// StatsTracker 隧道运行时统计追踪器（高内聚：统计逻辑独立于隧道转发逻辑）
// 实现 core.TunnelStatsReader 接口
type StatsTracker struct {
	mu    sync.RWMutex
	stats map[string]*statsEntry // tunnelName → stats
}

// NewStatsTracker 创建统计追踪器
func NewStatsTracker() *StatsTracker {
	return &StatsTracker{stats: make(map[string]*statsEntry)}
}

func (st *StatsTracker) getOrCreate(name string) *statsEntry {
	st.mu.RLock()
	s, ok := st.stats[name]
	st.mu.RUnlock()
	if ok {
		return s
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if s, ok = st.stats[name]; ok {
		return s
	}
	s = &statsEntry{}
	st.stats[name] = s
	return s
}

// RecordBytesIn 记录入站字节数
func (st *StatsTracker) RecordBytesIn(name string, n int64) {
	s := st.getOrCreate(name)
	atomic.AddInt64(&s.BytesIn, n)
	atomic.StoreInt64(&s.lastActivity, time.Now().UnixNano())
}

// RecordBytesOut 记录出站字节数
func (st *StatsTracker) RecordBytesOut(name string, n int64) {
	s := st.getOrCreate(name)
	atomic.AddInt64(&s.BytesOut, n)
	atomic.StoreInt64(&s.lastActivity, time.Now().UnixNano())
}

// ConnOpened 记录连接打开
func (st *StatsTracker) ConnOpened(name string) {
	s := st.getOrCreate(name)
	atomic.AddInt64(&s.TotalConns, 1)
	atomic.AddInt64(&s.ActiveConns, 1)
}

// ConnClosed 记录连接关闭
func (st *StatsTracker) ConnClosed(name string) {
	// 只在条目存在时递减：断连清理 Remove(sKey) 与在途连接的延迟关闭竞争时，
	// getOrCreate 会复活条目并写出 ActiveConns=-1 的僵尸统计
	st.mu.RLock()
	s, ok := st.stats[name]
	st.mu.RUnlock()
	if !ok {
		return
	}
	atomic.AddInt64(&s.ActiveConns, -1)
}

// toRuntimeStats 将内部条目转为 core.TunnelRuntimeStats（值拷贝，线程安全）
func toRuntimeStats(s *statsEntry) *core.TunnelRuntimeStats {
	lastNano := atomic.LoadInt64(&s.lastActivity)
	var lastActivity string
	if lastNano > 0 {
		lastActivity = time.Unix(0, lastNano).Format(time.RFC3339)
	}
	return &core.TunnelRuntimeStats{
		BytesIn:      atomic.LoadInt64(&s.BytesIn),
		BytesOut:     atomic.LoadInt64(&s.BytesOut),
		TotalConns:   atomic.LoadInt64(&s.TotalConns),
		ActiveConns:  atomic.LoadInt64(&s.ActiveConns),
		LastActivity: lastActivity,
	}
}

// Get 获取指定隧道的统计快照（实现 core.TunnelStatsReader）
func (st *StatsTracker) Get(name string) *core.TunnelRuntimeStats {
	st.mu.RLock()
	defer st.mu.RUnlock()
	if s, ok := st.stats[name]; ok {
		return toRuntimeStats(s)
	}
	return nil
}

// GetAll 获取所有隧道的统计快照（实现 core.TunnelStatsReader）
func (st *StatsTracker) GetAll() map[string]*core.TunnelRuntimeStats {
	st.mu.RLock()
	defer st.mu.RUnlock()
	result := make(map[string]*core.TunnelRuntimeStats, len(st.stats))
	for k, v := range st.stats {
		result[k] = toRuntimeStats(v)
	}
	return result
}

// Remove 清理离线节点的统计条目
func (st *StatsTracker) Remove(name string) {
	st.mu.Lock()
	delete(st.stats, name)
	st.mu.Unlock()
}
