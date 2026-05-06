package vpn

import (
	"context"
	"fmt"
	"log"
	"sync"
)

// Manager VPN 管理器（管理多个进程）
type Manager struct {
	mu      sync.RWMutex
	configs map[string]Config      // 所有 VPN 隧道配置
	procs   map[string]*ProcessMgr // 已创建的进程管理器
	ctx     context.Context
	cancel  context.CancelFunc
}

// NewManager 创建管理器
func NewManager(ctx context.Context) *Manager {
	ctx, cancel := context.WithCancel(ctx)
	return &Manager{
		configs: make(map[string]Config),
		procs:   make(map[string]*ProcessMgr),
		ctx:     ctx,
		cancel:  cancel,
	}
}

// Close 关闭管理器
func (m *Manager) Close() {
	if m.cancel != nil {
		m.cancel()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, pm := range m.procs {
		pm.Stop()
	}
}

// Create 创建并启动进程
func (m *Manager) Create(name string, cfg Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 互斥检查
	if _, ok := m.procs[name]; ok {
		return fmt.Errorf("vpn manager %q already exists", name)
	}

	pm, err := NewProcessMgr(name, cfg)
	if err != nil {
		return err
	}

	if err := pm.Start(m.ctx); err != nil {
		return err
	}

	m.procs[name] = pm
	return nil
}

// Start 启动指定进程（按需创建 ProcessMgr）
func (m *Manager) Start(name string) error {
	m.mu.Lock()
	pm, ok := m.procs[name]
	if !ok {
		cfg, cfgOk := m.configs[name]
		if !cfgOk {
			m.mu.Unlock()
			return fmt.Errorf("vpn manager %q not found", name)
		}
		var err error
		pm, err = NewProcessMgr(name, cfg)
		if err != nil {
			m.mu.Unlock()
			return fmt.Errorf("vpn manager %q create failed: %w", name, err)
		}
		m.procs[name] = pm
	}
	m.mu.Unlock()

	if pm.IsRunning() {
		return fmt.Errorf("vpn manager %q already running", name)
	}

	return pm.Start(m.ctx)
}

// Stop 停止指定进程
func (m *Manager) Stop(name string) error {
	m.mu.RLock()
	pm, ok := m.procs[name]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("vpn manager %q not found", name)
	}
	pm.Stop()
	return nil
}

// Status 获取状态
func (m *Manager) Status(name string) (Status, error) {
	m.mu.RLock()
	pm, ok := m.procs[name]
	m.mu.RUnlock()

	if !ok {
		return Status{}, fmt.Errorf("vpn manager %q not found", name)
	}
	return pm.Status(), nil
}

// List 列出所有进程
func (m *Manager) List() []Status {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]Status, 0, len(m.procs))
	for _, pm := range m.procs {
		result = append(result, pm.Status())
	}
	return result
}

// CrashLogs 获取崩溃日志
func (m *Manager) CrashLogs(name string) ([]CrashLog, error) {
	m.mu.RLock()
	pm, ok := m.procs[name]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("vpn manager %q not found", name)
	}
	return pm.CrashLogs(), nil
}

// VNTData 查询指定隧道的 vnt-cli REST API 数据
func (m *Manager) VNTData(name string) (*VNTInfo, []VNTDeviceItem, []VNTRouteItem, *VNTBuildInfo, error) {
	m.mu.RLock()
	pm, ok := m.procs[name]
	m.mu.RUnlock()

	if !ok {
		return nil, nil, nil, nil, fmt.Errorf("vpn manager %q not found", name)
	}
	info, peers, routes, status := pm.VNTData()
	return info, peers, routes, status, nil
}

// VNTChart 查询指定隧道的 vnt-cli 流量统计
func (m *Manager) VNTChart(name string) (*VNTChartA, error) {
	m.mu.RLock()
	pm, ok := m.procs[name]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("vpn manager %q not found", name)
	}
	return pm.VNTChart()
}

// OnTunnelUpdate 处理隧道更新（从 tunnel_push 触发）
func (m *Manager) OnTunnelUpdate(tunnelTypes []string, tunnelConfigs map[string]Config) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 构建需要管理的隧道名称映射
	needMgr := make(map[string]bool)
	for _, t := range tunnelTypes {
		needMgr[t] = true
	}

	// 更新配置映射：移除已删除的，保存现有的
	for name := range m.configs {
		if !needMgr[name] {
			delete(m.configs, name)
		}
	}
	for name, cfg := range tunnelConfigs {
		m.configs[name] = cfg
	}

	// 停止不再需要的进程
	for name, pm := range m.procs {
		if !needMgr[name] {
			log.Printf("vpn-manager: stopping removed tunnel %s", name)
			pm.Stop()
			delete(m.procs, name)
		}
	}

	// 启动新增的进程（如果配置了 autostart）
	for name, cfg := range tunnelConfigs {
		if _, ok := m.procs[name]; ok {
			continue // 已存在
		}
		if cfg.Lifecycle.Autostart {
			pm, err := NewProcessMgr(name, cfg)
			if err != nil {
				log.Printf("vpn-manager: create %s failed: %v", name, err)
				continue
			}
			if err := pm.Start(m.ctx); err != nil {
				log.Printf("vpn-manager: start %s failed: %v", name, err)
				continue
			}
			m.procs[name] = pm
		}
	}
}
