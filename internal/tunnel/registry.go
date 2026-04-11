package tunnel

import (
	"context"
	"log/slog"
	"net"
	"sync"

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
	// 关闭旧的
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

// 信号量实现
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

// TunnelGateway 隧道网关
type TunnelGateway struct {
	nodeMgr  interface {
		Get(ctx context.Context, nodeID string) (*core.Node, bool)
		GetAll(ctx context.Context) []*core.Node
	}
	registry *ListenerRegistry
	sem      *Semaphore
}

func NewTunnelGateway(nodeMgr interface {
	Get(ctx context.Context, nodeID string) (*core.Node, bool)
	GetAll(ctx context.Context) []*core.Node
}, maxConcurrent int) *TunnelGateway {
	return &TunnelGateway{
		nodeMgr:  nodeMgr,
		registry: NewListenerRegistry(),
		sem:      NewSemaphore(maxConcurrent),
	}
}

func (tg *TunnelGateway) Registry() *ListenerRegistry {
	return tg.registry
}

// Stop 停止所有隧道
func (tg *TunnelGateway) Stop() {
	tg.registry.StopAll()
}

// StopTunnel 停止指定名称的隧道
func (tg *TunnelGateway) StopTunnel(name string) {
	tg.registry.Unregister(name)
}
