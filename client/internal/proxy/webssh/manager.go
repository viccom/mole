package webssh

import (
	"io"
	"log/slog"
	"sync"
)

// Manager 管理多个 WebSSH 隧道的生命周期
type Manager struct {
	mu      sync.RWMutex
	tunnels map[string]*Handler
}

// NewManager 创建 WebSSH 管理器
func NewManager() *Manager {
	return &Manager{
		tunnels: make(map[string]*Handler),
	}
}

// Close 关闭所有 SSH 连接
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, h := range m.tunnels {
		h.Close()
		delete(m.tunnels, name)
	}
}

// Status 返回指定隧道的状态
func (m *Manager) Status(name string) (Stats, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if h, ok := m.tunnels[name]; ok {
		return h.Stats(), nil
	}
	return Stats{}, nil
}

// List 返回所有隧道状态
func (m *Manager) List() []Stats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]Stats, 0, len(m.tunnels))
	for _, h := range m.tunnels {
		result = append(result, h.Stats())
	}
	return result
}

// OnTunnelUpdate 增量更新隧道配置（由 client.notifyManagers 调用）
func (m *Manager) OnTunnelUpdate(configs map[string]WebSSHConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 关闭已移除的隧道
	for name, h := range m.tunnels {
		if _, ok := configs[name]; !ok {
			slog.Info("webssh stopping removed tunnel", "name", name)
			h.Close()
			delete(m.tunnels, name)
		}
	}

	// 启动或重启隧道
	for name, cfg := range configs {
		if !cfg.Enable {
			if h, ok := m.tunnels[name]; ok {
				slog.Info("webssh stopping disabled tunnel", "name", name)
				h.Close()
				delete(m.tunnels, name)
			}
			continue
		}

		if existing, ok := m.tunnels[name]; ok {
			// 配置未变则跳过
			if existing.cfg.Equal(cfg) && existing.running.Load() {
				continue
			}
			slog.Info("webssh restarting tunnel", "name", name)
			existing.Close()
			delete(m.tunnels, name)
		}

		if err := cfg.Validate(); err != nil {
			slog.Warn("webssh invalid config", "name", name, "error", err)
			continue
		}

		h := newHandler(name, cfg)
		m.tunnels[name] = h
		slog.Info("webssh registered tunnel", "name", name, "user", cfg.User, "host", cfg.Host, "port", cfg.Port)
	}
}

// HandleStream 将 smux 流分发到对应的 WebSSH handler
func (m *Manager) HandleStream(tunnelName string, stream io.ReadWriteCloser) {
	m.mu.RLock()
	h, ok := m.tunnels[tunnelName]
	m.mu.RUnlock()

	if !ok {
		slog.Warn("webssh tunnel not found, closing stream", "name", tunnelName)
		stream.Close()
		return
	}

	h.HandleStream(stream)
}
