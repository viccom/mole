package ser2mq

import (
	"context"
	"fmt"
	"log"
	"sync"
)

// Manager ser2mq 隧道管理器
type Manager struct {
	mu      sync.RWMutex
	tunnels map[string]*Ser2MQHandler
	nodeID  string
	ctx     context.Context
	cancel  context.CancelFunc
}

// NewManager 创建管理器
func NewManager(ctx context.Context) *Manager {
	ctx, cancel := context.WithCancel(ctx)
	return &Manager{
		tunnels: make(map[string]*Ser2MQHandler),
		ctx:     ctx,
		cancel:  cancel,
	}
}

// SetNodeID 设置节点 ID（在 Run() 时调用）
func (m *Manager) SetNodeID(nodeID string) {
	m.nodeID = nodeID
}

// Close 关闭管理器
func (m *Manager) Close() {
	if m.cancel != nil {
		m.cancel()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, h := range m.tunnels {
		h.Stop()
	}
}

// Create 创建并启动 ser2mq 隧道
func (m *Manager) Create(name, nodeID string, cfg Ser2MQConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 同名检查
	if _, ok := m.tunnels[name]; ok {
		return fmt.Errorf("ser2mq tunnel %q already exists", name)
	}

	// 同一串口检查
	for _, h := range m.tunnels {
		if h.cfg.Serial.Port == cfg.Serial.Port {
			return fmt.Errorf("ser2mq tunnel for serial port %q already exists", cfg.Serial.Port)
		}
	}

	handler, err := NewHandler(name, nodeID, cfg)
	if err != nil {
		return err
	}

	if err := handler.Start(m.ctx); err != nil {
		return err
	}

	m.tunnels[name] = handler
	return nil
}

// Stop 停止指定隧道
func (m *Manager) Stop(name string) error {
	m.mu.Lock()
	handler, ok := m.tunnels[name]
	if ok {
		handler.Stop()
		delete(m.tunnels, name)
	}
	m.mu.Unlock()

	if !ok {
		return fmt.Errorf("ser2mq tunnel %q not found", name)
	}
	return nil
}

// Status 获取状态
func (m *Manager) Status(name string) (Ser2MQStats, error) {
	m.mu.RLock()
	handler, ok := m.tunnels[name]
	m.mu.RUnlock()

	if !ok {
		return Ser2MQStats{}, fmt.Errorf("ser2mq tunnel %q not found", name)
	}
	return handler.Stats(), nil
}

// List 列出所有隧道状态
func (m *Manager) List() []Ser2MQStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]Ser2MQStats, 0, len(m.tunnels))
	for _, h := range m.tunnels {
		result = append(result, h.Stats())
	}
	return result
}

// OnTunnelUpdate 处理隧道更新（从 tunnel_push 触发）
func (m *Manager) OnTunnelUpdate(tunnelConfigs map[string]Ser2MQConfig) {
	m.mu.Lock()

	// 停止不再需要的隧道
	for name, h := range m.tunnels {
		if _, ok := tunnelConfigs[name]; !ok {
			log.Printf("ser2mq: stopping removed tunnel %s", name)
			h.Stop()
			delete(m.tunnels, name)
		}
	}

	// 收集需要创建的隧道（还需要 nodeID）
	type pending struct {
		name string
		cfg  Ser2MQConfig
	}
	var pendings []pending
	for name, cfg := range tunnelConfigs {
		if _, ok := m.tunnels[name]; !ok {
			pendings = append(pendings, pending{name, cfg})
		}
	}
	m.mu.Unlock()

	// 在锁外逐个创建（Create 内部会重入锁做同名检查）
	for _, p := range pendings {
		if err := m.Create(p.name, m.nodeID, p.cfg); err != nil {
			log.Printf("ser2mq: failed to create tunnel %s: %v", p.name, err)
		}
	}
}
