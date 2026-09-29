package tunnel

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"

	"github.com/xtaci/smux"

	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/ratelimit"
)

// 确保 StatsTracker 实现 core.TunnelStatsReader 接口
var _ core.TunnelStatsReader = (*StatsTracker)(nil)

// tunnelRuntime 隧道运行时生命周期句柄
type tunnelRuntime struct {
	cancel func()        // 取消隧道级 context
	done   chan struct{} // 运行循环退出信号
}

// ListenerRegistry 管理所有隧道的监听器和运行时
type ListenerRegistry struct {
	mu        sync.RWMutex
	listeners map[string]net.Listener   // tunnelName -> listener
	runtimes  map[string]*tunnelRuntime // tunnelName -> runtime handle
}

func NewListenerRegistry() *ListenerRegistry {
	return &ListenerRegistry{
		listeners: make(map[string]net.Listener),
		runtimes:  make(map[string]*tunnelRuntime),
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

// RegisterRuntime 注册隧道运行时句柄
func (lr *ListenerRegistry) RegisterRuntime(name string, rt *tunnelRuntime) {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	// 如果已有旧 runtime，先取消
	if old, ok := lr.runtimes[name]; ok {
		old.cancel()
	}
	lr.runtimes[name] = rt
}

// Unregister 注销并关闭监听器和运行时
func (lr *ListenerRegistry) Unregister(name string) {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	if l, ok := lr.listeners[name]; ok {
		l.Close()
		delete(lr.listeners, name)
	}
	if rt, ok := lr.runtimes[name]; ok {
		rt.cancel()
		delete(lr.runtimes, name)
	}
}

// StopTunnel 停止指定隧道：先 cancel runtime，再关闭 listener，最后等待退出
func (lr *ListenerRegistry) StopTunnel(name string) {
	lr.mu.Lock()
	var done chan struct{}
	if rt, ok := lr.runtimes[name]; ok {
		rt.cancel()
		done = rt.done
		delete(lr.runtimes, name)
	}
	if l, ok := lr.listeners[name]; ok {
		l.Close()
		delete(lr.listeners, name)
	}
	lr.mu.Unlock()

	// 在锁外等待运行循环退出（避免死锁）
	if done != nil {
		<-done
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

// StopAll 关闭所有监听器和运行时
func (lr *ListenerRegistry) StopAll() {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	for name, l := range lr.listeners {
		l.Close()
		slog.Info("Listener stopped", "tunnel", name)
	}
	for name, rt := range lr.runtimes {
		rt.cancel()
		slog.Info("Runtime cancelled", "tunnel", name)
	}
	lr.listeners = make(map[string]net.Listener)
	lr.runtimes = make(map[string]*tunnelRuntime)
}

// isClosedConnError 判断是否为连接已关闭类错误（用于优雅退出判断）
func isClosedConnError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, net.ErrClosed) {
		return true
	}
	// UDP/TCP Accept/ReadFrom 返回的 "use of closed network connection"
	if opErr, ok := err.(*net.OpError); ok {
		return opErr.Err.Error() == "use of closed network connection"
	}
	return false
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
	stats    *StatsTracker
	limiter  ratelimit.GatewayLimiter

	// 路由索引：加速域名和隧道名称查找。
	// 双索引共用一把锁（QUA-01）：RebuildIndex 在同一临界区内完成两索引
	// 替换，消除读侧看到「新 domain + 旧 tunnel」混搭快照的窗口
	idxMu     sync.RWMutex
	domainIdx map[string]*domainRoute // domain -> route
	tunnelIdx map[string]*tunnelRoute // tunnelName -> route

	// HyphenRouting 泛域名分隔符：true=hyphen(-), false=dot(.)
	HyphenRouting bool
}

type domainRoute struct {
	nodeID     string
	tunnelName string
}

type tunnelRoute struct {
	nodeID string
}

func NewTunnelGateway(nodeMgr NodeProvider, maxConcurrent int, limiter ratelimit.GatewayLimiter) *TunnelGateway {
	if limiter == nil {
		limiter = ratelimit.NopLimiter{}
	}
	return &TunnelGateway{
		nodeMgr:       nodeMgr,
		registry:      NewListenerRegistry(),
		sem:           NewSemaphore(maxConcurrent),
		stats:         NewStatsTracker(),
		limiter:       limiter,
		domainIdx:     make(map[string]*domainRoute),
		tunnelIdx:     make(map[string]*tunnelRoute),
		HyphenRouting: true,
	}
}

func (tg *TunnelGateway) Registry() *ListenerRegistry {
	return tg.registry
}

// Stats 返回统计追踪器（实现 routeIndexer 接口）
func (tg *TunnelGateway) Stats() core.TunnelStatsReader {
	return tg.stats
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
			if !t.IsEnabled() {
				continue
			}
			newTunnel[t.Name] = &tunnelRoute{nodeID: n.ID}
			if isDomainRoutable(t) {
				newDomain[t.Domain] = &domainRoute{nodeID: n.ID, tunnelName: t.Name}
			}
		}
	}

	// QUA-01：两索引在同一临界区内一次换入，读者不会观察到混搭快照
	tg.idxMu.Lock()
	tg.domainIdx = newDomain
	tg.tunnelIdx = newTunnel
	tg.idxMu.Unlock()
}

// findByDomain 通过域名索引查找节点和隧道名
func (tg *TunnelGateway) findByDomain(ctx context.Context, domain string) (*core.Node, string) {
	tg.idxMu.RLock()
	r, ok := tg.domainIdx[domain]
	tg.idxMu.RUnlock()
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
	tg.idxMu.RLock()
	r, ok := tg.tunnelIdx[tunnelName]
	tg.idxMu.RUnlock()
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
	tg.registry.StopTunnel(name)
}
