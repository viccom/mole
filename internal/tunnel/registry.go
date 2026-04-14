package tunnel

import (
	"context"
	"log/slog"
	"net"
	"sync"

	"github.com/xtaci/smux"

	"moleAgent_Serv/internal/core"
)

// ListenerRegistry 管理所有隧道的监听器
type ListenerRegistry struct {
	mu        sync.RWMutex
	listeners map[string]net.Listener // tunnelName -> listener
}

func NewListenerRegistry() *ListenerRegistry {
	return &ListenerRegistry{
		listeners: make(map[string]net.Listener),
	}
}

// Register 注册一个监听器
func (lr *ListenerRegistry) Register(name string, l net.Listener) {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	if old, ok := lr.listeners[name]; ok {
		old.Close()
	}
	lr.listeners[name] = l
}

// Unregister 注销并关闭监听器
func (lr *ListenerRegistry) Unregister(name string) {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	if l, ok := lr.listeners[name]; ok {
		l.Close()
		delete(lr.listeners, name)
	}
}

// Get 获取监听器
func (lr *ListenerRegistry) Get(name string) (net.Listener, bool) {
	lr.mu.RLock()
	defer lr.mu.RUnlock()
	l, ok := lr.listeners[name]
	return l, ok
}

// List 返回所有监听器名
func (lr *ListenerRegistry) List() []string {
	lr.mu.RLock()
	defer lr.mu.RUnlock()
	names := make([]string, 0, len(lr.listeners))
	for name := range lr.listeners {
		names = append(names, name)
	}
	return names
}

// StopAll 关闭所有监听器
func (lr *ListenerRegistry) StopAll() {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	for name, l := range lr.listeners {
		l.Close()
		slog.Info("Listener stopped", "tunnel", name)
	}
	lr.listeners = make(map[string]net.Listener)
}

// Semaphore 信号量实现
type Semaphore struct {
	ch chan struct{}
}

func NewSemaphore(max int) *Semaphore {
	return &Semaphore{ch: make(chan struct{}, max)}
}

func (s *Semaphore) Acquire(ctx context.Context) error {
	select {
	case s.ch <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Semaphore) Release() {
	<-s.ch
}

// NodeProvider 节点只读查询接口（由 tunnel 包定义，解耦具体实现）
type NodeProvider interface {
	Get(ctx context.Context, nodeID string) (*core.Node, bool)
	GetAll(ctx context.Context) []*core.Node
	GetSession(ctx context.Context, nodeID string) (*smux.Session, error)
}

// TunnelGateway 隧道网关
type TunnelGateway struct {
	nodeMgr  NodeProvider
	registry *ListenerRegistry
	sem      *Semaphore

	// 路由索引：加速域名和隧道名称查找
	domainMu sync.RWMutex
	domainIdx map[string]*domainRoute // domain -> route

	tunnelMu sync.RWMutex
	tunnelIdx map[string]*tunnelRoute // tunnelName -> route
}

type domainRoute struct {
	nodeID     string
	tunnelName string
}

type tunnelRoute struct {
	nodeID string
}

func NewTunnelGateway(nodeMgr NodeProvider, maxConcurrent int) *TunnelGateway {
	return &TunnelGateway{
		nodeMgr:   nodeMgr,
		registry:  NewListenerRegistry(),
		sem:       NewSemaphore(maxConcurrent),
		domainIdx: make(map[string]*domainRoute),
		tunnelIdx: make(map[string]*tunnelRoute),
	}
}

func (tg *TunnelGateway) Registry() *ListenerRegistry {
	return tg.registry
}

// RebuildIndex 根据当前节点数据重建路由索引
func (tg *TunnelGateway) RebuildIndex(ctx context.Context) {
	nodes := tg.nodeMgr.GetAll(ctx)

	newDomain := make(map[string]*domainRoute)
	newTunnel := make(map[string]*tunnelRoute)

	for _, n := range nodes {
		if n.Status != core.NodeStatusOnline {
			continue
		}
		for _, t := range n.Tunnels {
			newTunnel[t.Name] = &tunnelRoute{nodeID: n.ID}
			if t.Type == core.TunnelTypeHTTP && t.Domain != "" {
				newDomain[t.Domain] = &domainRoute{nodeID: n.ID, tunnelName: t.Name}
			}
		}
	}

	tg.domainMu.Lock()
	tg.domainIdx = newDomain
	tg.domainMu.Unlock()

	tg.tunnelMu.Lock()
	tg.tunnelIdx = newTunnel
	tg.tunnelMu.Unlock()
}

// findByDomain 通过域名索引查找节点和隧道名
func (tg *TunnelGateway) findByDomain(ctx context.Context, domain string) (*core.Node, string) {
	tg.domainMu.RLock()
	r, ok := tg.domainIdx[domain]
	tg.domainMu.RUnlock()
	if !ok {
		return nil, ""
	}
	node, exists := tg.nodeMgr.Get(ctx, r.nodeID)
	if !exists || node.Status != core.NodeStatusOnline {
		return nil, ""
	}
	return node, r.tunnelName
}

// findByTunnelName 通过隧道名称索引查找节点
func (tg *TunnelGateway) findByTunnelName(ctx context.Context, tunnelName string) *core.Node {
	tg.tunnelMu.RLock()
	r, ok := tg.tunnelIdx[tunnelName]
	tg.tunnelMu.RUnlock()
	if !ok {
		return nil
	}
	node, exists := tg.nodeMgr.Get(ctx, r.nodeID)
	if !exists || node.Status != core.NodeStatusOnline {
		return nil
	}
	return node
}

// Stop 停止所有隧道
func (tg *TunnelGateway) Stop() {
	tg.registry.StopAll()
}

// StopTunnel 停止指定名称的隧道
func (tg *TunnelGateway) StopTunnel(name string) {
	tg.registry.Unregister(name)
}
