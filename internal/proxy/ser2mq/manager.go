package ser2mq

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
)

// Manager ser2mq 隧道管理器
type Manager struct {
	mu      sync.RWMutex
	tunnels map[string]*Ser2MQHandler
	errors  map[string]Ser2MQStats // 启动失败的隧道错误信息
	nodeID  string
	ctx     context.Context
	cancel  context.CancelFunc

	streamHub *StreamHub

	newHandler   func(name, nodeID string, cfg Ser2MQConfig) (*Ser2MQHandler, error)
	startHandler func(handler *Ser2MQHandler, ctx context.Context) error
	stopHandler  func(handler *Ser2MQHandler)
}

// NewManager 创建管理器
func NewManager(ctx context.Context, nodeID string) *Manager {
	ctx, cancel := context.WithCancel(ctx)
	hub := NewStreamHub(200)
	return &Manager{
		tunnels:   make(map[string]*Ser2MQHandler),
		errors:    make(map[string]Ser2MQStats),
		nodeID:    nodeID,
		ctx:       ctx,
		cancel:    cancel,
		streamHub: hub,
		newHandler: func(name, nodeID string, cfg Ser2MQConfig) (*Ser2MQHandler, error) {
			return NewHandler(name, nodeID, cfg, hub)
		},
		startHandler: func(handler *Ser2MQHandler, ctx context.Context) error {
			return handler.Start(ctx)
		},
		stopHandler: func(handler *Ser2MQHandler) {
			handler.Stop()
		},
	}
}

// StreamHub 返回事件 hub
func (m *Manager) StreamHub() *StreamHub {
	return m.streamHub
}

// SetNodeID 设置节点 ID（在 Run() 时调用，确保 nodeID 就绪）
func (m *Manager) SetNodeID(nodeID string) {
	m.mu.Lock()
	m.nodeID = nodeID
	m.mu.Unlock()
}

// detectErrorPhase 根据错误判断失败阶段
func (m *Manager) detectErrorPhase(err error) string {
	errStr := err.Error()
	if strings.Contains(errStr, "serial") || strings.Contains(errStr, "port") || strings.Contains(errStr, "open") {
		return "serial"
	}
	if strings.Contains(errStr, "mqtt") || strings.Contains(errStr, "connect") || strings.Contains(errStr, "broker") {
		return "mqtt"
	}
	return "unknown"
}

// Close 关闭管理器
func (m *Manager) Close() {
	if m.cancel != nil {
		m.cancel()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, h := range m.tunnels {
		m.stopHandler(h)
	}
}

// Create 创建并启动 ser2mq 隧道
func (m *Manager) Create(name string, cfg Ser2MQConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.tunnels[name]; ok {
		return fmt.Errorf("ser2mq tunnel %q already exists", name)
	}

	for _, h := range m.tunnels {
		if h.cfg.Serial.Port == cfg.Serial.Port {
			return fmt.Errorf("ser2mq tunnel for serial port %q already exists", cfg.Serial.Port)
		}
	}

	handler, err := m.newHandler(name, m.nodeID, cfg)
	if err != nil {
		return err
	}

	if err := m.startHandler(handler, m.ctx); err != nil {
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
		m.stopHandler(handler)
		delete(m.tunnels, name)
	}
	delete(m.errors, name)
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
	if ok {
		stats := handler.Stats()
		m.mu.RUnlock()
		return stats, nil
	}
	// 返回 errors map 中存储的启动失败信息
	if errStats, hasError := m.errors[name]; hasError {
		m.mu.RUnlock()
		return errStats, nil
	}
	m.mu.RUnlock()
	return Ser2MQStats{}, fmt.Errorf("ser2mq tunnel %q not found", name)
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
// 停止已删除的隧道，启动新增的隧道
func (m *Manager) OnTunnelUpdate(tunnelConfigs map[string]Ser2MQConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 停止不再需要的隧道
	for name, h := range m.tunnels {
		cfg, ok := tunnelConfigs[name]
		if !ok || !cfg.Enable {
			log.Printf("ser2mq: stopping removed tunnel %s", name)
			m.stopHandler(h)
			delete(m.tunnels, name)
			delete(m.errors, name)
		}
	}

	// 启动新增隧道，并在配置变化时重建已有隧道
	for name, cfg := range tunnelConfigs {
		if !cfg.Enable {
			continue // 未启用
		}

		if existing, ok := m.tunnels[name]; ok {
			if existing.cfg == cfg && existing.nodeID == m.nodeID && existing.IsRunning() {
				continue
			}
			log.Printf("ser2mq: restarting tunnel %s to apply updated config", name)
			m.stopHandler(existing)
			delete(m.tunnels, name)
			delete(m.errors, name)
		}

		handler, err := m.newHandler(name, m.nodeID, cfg)
		if err != nil {
			log.Printf("ser2mq: create handler %s error: %v", name, err)
			m.errors[name] = Ser2MQStats{
				Name:       name,
				Broker:     cfg.Broker,
				SerialPort: cfg.Serial.Port,
				Error:      err.Error(),
				ErrorPhase: "serial",
			}
			continue
		}

		if err := m.startHandler(handler, m.ctx); err != nil {
			log.Printf("ser2mq: start tunnel %s error: %v", name, err)
			m.errors[name] = Ser2MQStats{
				Name:       name,
				Broker:     cfg.Broker,
				SerialPort: cfg.Serial.Port,
				Error:      err.Error(),
				ErrorPhase: m.detectErrorPhase(err),
			}
			continue
		}

		delete(m.errors, name)
		m.tunnels[name] = handler
		log.Printf("ser2mq: started tunnel %s (serial: %s)", name, cfg.Serial.Port)
	}
}
