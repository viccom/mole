package node

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Node 表示一个 moleAgent_client 后端节点
type Node struct {
	Name       string `json:"name"`       // 节点名称（显示用）
	Addr       string `json:"addr"`       // 后端地址
	Port       int    `json:"port"`       // 后端端口
	IsDefault  bool   `json:"is_default"` // 是否为默认节点
	IsOnline   bool   `json:"is_online"`   // 在线状态（运行时）
}

// Config 节点配置文件结构
type Config struct {
	Nodes        []Node `json:"nodes"`        // 所有节点列表
	CurrentNode  string `json:"current_node"`  // 当前激活的节点名称
	CheckInterval int   `json:"check_interval"` // 检测间隔（秒）
}

const defaultNodePort = 59870

// Manager 节点管理器
type Manager struct {
	mu      sync.RWMutex
	config  *Config
	cfgPath string
}

// NewManager 创建节点管理器
func NewManager() *Manager {
	cfgPath := getConfigPath()
	return &Manager{
		cfgPath: cfgPath,
		config: &Config{
			Nodes:        []Node{},
			CurrentNode:  "",
			CheckInterval: 5,
		},
	}
}

// Load 从文件加载配置
func (m *Manager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			// 文件不存在，使用默认配置（本地）
			m.config.Nodes = []Node{
				{
					Name:      "本地",
					Addr:      "127.0.0.1",
					Port:      defaultNodePort,
					IsDefault: true,
				},
			}
			m.config.CurrentNode = "本地"
			return nil
		}
		return fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	normalizeConfig(&cfg)

	m.config = &cfg
	return nil
}

// Save 保存配置到文件
func (m *Manager) Save() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// 确保目录存在
	dir := filepath.Dir(m.cfgPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	data, err := json.MarshalIndent(m.config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	if err := os.WriteFile(m.cfgPath, data, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}

// List 返回所有节点
func (m *Manager) List() []Node {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]Node, len(m.config.Nodes))
	copy(result, m.config.Nodes)
	return result
}

// GetNode 根据名称获取节点
func (m *Manager) GetNode(name string) *Node {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for i := range m.config.Nodes {
		if m.config.Nodes[i].Name == name {
			return &m.config.Nodes[i]
		}
	}
	return nil
}

// GetCurrentNode 获取当前激活的节点
func (m *Manager) GetCurrentNode() *Node {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.config.CurrentNode == "" {
		// 返回默认节点
		for i := range m.config.Nodes {
			if m.config.Nodes[i].IsDefault {
				return &m.config.Nodes[i]
			}
		}
		// 返回第一个
		if len(m.config.Nodes) > 0 {
			return &m.config.Nodes[0]
		}
		return nil
	}

	for i := range m.config.Nodes {
		if m.config.Nodes[i].Name == m.config.CurrentNode {
			return &m.config.Nodes[i]
		}
	}

	return nil
}

// SetCurrentNode 设置当前节点
func (m *Manager) SetCurrentNode(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config.CurrentNode = name
}

// GetCheckInterval 获取检测间隔
func (m *Manager) GetCheckInterval() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.config.CheckInterval <= 0 {
		return 5
	}
	return m.config.CheckInterval
}

// GetDefaultNode 获取默认节点
func (m *Manager) GetDefaultNode() *Node {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for i := range m.config.Nodes {
		if m.config.Nodes[i].IsDefault {
			return &m.config.Nodes[i]
		}
	}
	return nil
}

// Add 添加新节点
func (m *Manager) Add(n Node) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 检查名称是否已存在
	for i := range m.config.Nodes {
		if m.config.Nodes[i].Name == n.Name {
			return fmt.Errorf("node %q already exists", n.Name)
		}
	}

	// 清理和验证地址
	host, port, err := normalizeEndpoint(n.Addr, n.Port)
	if err != nil {
		return err
	}
	n.Addr = host
	n.Port = port

	// 设置默认值
	if n.Port == 0 {
		n.Port = defaultNodePort
	}
	if len(m.config.Nodes) == 0 {
		n.IsDefault = true
	}
	if n.IsDefault {
		clearDefaultNode(m.config.Nodes)
	}

	m.config.Nodes = append(m.config.Nodes, n)
	ensureConfigInvariants(m.config)
	return nil
}

// Update 更新节点
func (m *Manager) Update(name string, n Node) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range m.config.Nodes {
		if m.config.Nodes[i].Name == name {
			host, port, err := normalizeEndpoint(n.Addr, n.Port)
			if err != nil {
				return err
			}
			n.Addr = host
			n.Port = port
			if n.Port == 0 {
				n.Port = defaultNodePort
			}
			// 保留原始名称
			n.Name = name
			if n.IsDefault {
				clearDefaultNode(m.config.Nodes)
			}
			m.config.Nodes[i] = n
			ensureConfigInvariants(m.config)
			return nil
		}
	}

	return fmt.Errorf("node %q not found", name)
}

// Remove 移除节点
func (m *Manager) Remove(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range m.config.Nodes {
		if m.config.Nodes[i].Name == name {
			m.config.Nodes = append(m.config.Nodes[:i], m.config.Nodes[i+1:]...)

			// 如果删除的是当前节点，清除当前节点
			if m.config.CurrentNode == name {
				m.config.CurrentNode = ""
			}
			ensureConfigInvariants(m.config)
			return nil
		}
	}

	return fmt.Errorf("node %q not found", name)
}

// GetURL 获取节点的 Web UI URL
func (n *Node) GetURL() string {
	return fmt.Sprintf("http://%s/ui", net.JoinHostPort(n.Addr, strconv.Itoa(n.Port)))
}

// GetHealthURL 获取健康检查 URL
func (n *Node) GetHealthURL() string {
	return fmt.Sprintf("http://%s/health", net.JoinHostPort(n.Addr, strconv.Itoa(n.Port)))
}

// getConfigPath 获取配置文件路径
func getConfigPath() string {
	if appData := os.Getenv("APPDATA"); appData != "" {
		return filepath.Join(appData, "moleAgent-manager", "config.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".moleAgent-manager", "config.json")
}

func normalizeConfig(cfg *Config) {
	if cfg.CheckInterval <= 0 {
		cfg.CheckInterval = 5
	}
	for i := range cfg.Nodes {
		host, port, err := normalizeEndpoint(cfg.Nodes[i].Addr, cfg.Nodes[i].Port)
		if err != nil {
			continue
		}
		cfg.Nodes[i].Addr = host
		cfg.Nodes[i].Port = port
	}
	ensureConfigInvariants(cfg)
}

func ensureConfigInvariants(cfg *Config) {
	if len(cfg.Nodes) == 0 {
		cfg.CurrentNode = ""
		return
	}

	defaultIndex := -1
	for i := range cfg.Nodes {
		if cfg.Nodes[i].IsDefault {
			defaultIndex = i
		}
	}
	if defaultIndex == -1 {
		defaultIndex = 0
		cfg.Nodes[0].IsDefault = true
	}
	for i := range cfg.Nodes {
		cfg.Nodes[i].IsDefault = i == defaultIndex
	}

	if cfg.CurrentNode == "" || !hasNode(cfg.Nodes, cfg.CurrentNode) {
		cfg.CurrentNode = cfg.Nodes[defaultIndex].Name
	}
}

func clearDefaultNode(nodes []Node) {
	for i := range nodes {
		nodes[i].IsDefault = false
	}
}

func hasNode(nodes []Node, name string) bool {
	for i := range nodes {
		if nodes[i].Name == name {
			return true
		}
	}
	return false
}

func normalizeEndpoint(raw string, fallbackPort int) (string, int, error) {
	addr := strings.TrimSpace(raw)
	if addr == "" {
		return "", 0, fmt.Errorf("addr is required")
	}

	if strings.Contains(addr, "://") {
		parsed, err := url.Parse(addr)
		if err != nil {
			return "", 0, fmt.Errorf("invalid addr %q", raw)
		}
		addr = parsed.Host
	}
	addr = strings.TrimSuffix(addr, "/")

	if host, port, ok := splitHostPort(addr); ok {
		return host, port, nil
	}

	if fallbackPort == 0 {
		fallbackPort = defaultNodePort
	}
	return stripBrackets(addr), fallbackPort, nil
}

func splitHostPort(addr string) (string, int, bool) {
	if host, portText, err := net.SplitHostPort(addr); err == nil {
		port, err := strconv.Atoi(portText)
		if err != nil || port <= 0 || port > 65535 {
			return "", 0, false
		}
		return stripBrackets(host), port, true
	}

	if strings.Count(addr, ":") == 1 && !strings.Contains(addr, "]") {
		parts := strings.SplitN(addr, ":", 2)
		port, err := strconv.Atoi(parts[1])
		if err != nil || port <= 0 || port > 65535 {
			return "", 0, false
		}
		return stripBrackets(parts[0]), port, true
	}

	return "", 0, false
}

func stripBrackets(host string) string {
	return strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
}
