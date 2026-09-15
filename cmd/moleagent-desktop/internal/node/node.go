package node

import (
	"crypto/rand"
	"encoding/json"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Node 表示一个服务端节点配置
type Node struct {
	ID         string `json:"id"`          // 内部唯一标识，前端无需展示
	Name       string `json:"name"`        // 节点名称（本地别名/显示用）
	NodeName   string `json:"node_name"`   // 客户端连接名称，对应 moleAgent_client -name
	ServerAddr string `json:"server_addr"` // 服务端地址
	Token      string `json:"token"`       // 认证令牌
	Transport  string `json:"transport"`   // 传输协议: tcp, ws, kcp
	TLS        bool   `json:"tls"`         // 是否启用 TLS
	IsDefault  bool   `json:"is_default"`  // 是否为默认节点
}

// Config 节点配置文件结构
type Config struct {
	Nodes       []Node `json:"nodes"`        // 所有节点列表
	CurrentNode string `json:"current_node"` // 当前激活节点 ID（兼容旧配置中的别名）
	BuiltinHTTP string `json:"builtin_http"` // 内置 HTTP 端口
}

// Manager 节点管理器
type Manager struct {
	mu         sync.RWMutex
	config     *Config
	cfgPath    string
	loadFailed bool // Load 失败时置位：防止 shutdown 用空配置覆盖原文件
}

var defaultBuiltinHTTP = "127.0.0.1:59870"

// NewManager 创建节点管理器
func NewManager() *Manager {
	cfgPath := getConfigPath()
	return &Manager{
		cfgPath: cfgPath,
		config: &Config{
			Nodes:       []Node{},
			CurrentNode: "",
			BuiltinHTTP: defaultBuiltinHTTP,
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
			// 文件不存在，使用默认配置
			return nil
		}
		m.loadFailed = true
		return fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		m.loadFailed = true
		return fmt.Errorf("parse config: %w", err)
	}
	normalizeConfig(&cfg)

	m.config = &cfg
	return nil
}

// Save 保存配置到文件
func (m *Manager) Save() error {
	// 写操作必须持写锁：RWMutex 只隔离 Save 与 mutator，不隔离 Save 与 Save
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.loadFailed {
		// 配置加载失败时内存里是空默认值：落盘会把用户的节点表（含 token）永久覆盖掉
		return fmt.Errorf("config %s failed to load, refusing to overwrite", m.cfgPath)
	}

	// 确保目录存在
	dir := filepath.Dir(m.cfgPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	data, err := json.MarshalIndent(m.config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	// 0600：配置含节点 token；临时文件+rename 原子替换，避免中途崩溃留下截断文件
	tmp := m.cfgPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := os.Rename(tmp, m.cfgPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename config: %w", err)
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

// GetNode 根据 ID 获取节点
func (m *Manager) GetNode(id string) *Node {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for i := range m.config.Nodes {
		if m.config.Nodes[i].ID == id {
			return &m.config.Nodes[i]
		}
	}
	return nil
}

// GetCurrentNode 获取当前激活的节点
func (m *Manager) GetCurrentNode() *Node {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// 如果当前节点为空，返回默认节点
	if m.config.CurrentNode == "" {
		for i := range m.config.Nodes {
			if m.config.Nodes[i].IsDefault {
				return &m.config.Nodes[i]
			}
		}
		// 如果没有默认节点，返回第一个
		if len(m.config.Nodes) > 0 {
			return &m.config.Nodes[0]
		}
		return nil
	}

	// 查找当前节点
	for i := range m.config.Nodes {
		if m.config.Nodes[i].ID == m.config.CurrentNode {
			return &m.config.Nodes[i]
		}
	}

	return nil
}

// SetCurrentNode 设置当前节点 ID
func (m *Manager) SetCurrentNode(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config.CurrentNode = id
}

// Add 添加新节点
func (m *Manager) Add(n Node) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 设置默认值
	if n.Transport == "" {
		n.Transport = "tcp"
	}
	if n.ID == "" {
		n.ID = generateNodeID()
	}
	for hasNodeID(m.config.Nodes, n.ID) {
		n.ID = generateNodeID()
	}

	m.config.Nodes = append(m.config.Nodes, n)
	return nil
}

// Update 更新节点
func (m *Manager) Update(id string, n Node) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range m.config.Nodes {
		if m.config.Nodes[i].ID == id {
			n.ID = id
			if n.Transport == "" {
				n.Transport = "tcp"
			}
			if n.Name == "" {
				n.Name = m.config.Nodes[i].Name
			}
			m.config.Nodes[i] = n
			return nil
		}
	}

	return fmt.Errorf("node %q not found", id)
}

// Remove 移除节点
func (m *Manager) Remove(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range m.config.Nodes {
		if m.config.Nodes[i].ID == id {
			m.config.Nodes = append(m.config.Nodes[:i], m.config.Nodes[i+1:]...)

			// 如果删除的是当前节点，清除当前节点
			if m.config.CurrentNode == id {
				m.config.CurrentNode = ""
			}
			return nil
		}
	}

	return fmt.Errorf("node %q not found", id)
}

// GetBuiltinHTTP 获取内置 HTTP 端口
func (m *Manager) GetBuiltinHTTP() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.BuiltinHTTP
}

// SetBuiltinHTTP 设置内置 HTTP 端口
func (m *Manager) SetBuiltinHTTP(port string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config.BuiltinHTTP = port
}

// getConfigPath 获取配置文件路径
func getConfigPath() string {
	// Windows: %APPDATA%\moleAgent-desktop\config.json
	// 尝试使用 APPDATA 环境变量
	if appData := os.Getenv("APPDATA"); appData != "" {
		return filepath.Join(appData, "moleAgent-desktop", "config.json")
	}

	// Fallback 到用户目录
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".moleAgent-desktop", "config.json")
}

func normalizeConfig(cfg *Config) {
	for i := range cfg.Nodes {
		if cfg.Nodes[i].Transport == "" {
			cfg.Nodes[i].Transport = "tcp"
		}
		if cfg.Nodes[i].ID == "" || hasNodeID(cfg.Nodes[:i], cfg.Nodes[i].ID) {
			cfg.Nodes[i].ID = uniqueNodeID(cfg.Nodes, i)
		}
	}

	if cfg.CurrentNode == "" {
		return
	}
	for i := range cfg.Nodes {
		if cfg.Nodes[i].ID == cfg.CurrentNode {
			return
		}
	}
	for i := range cfg.Nodes {
		if cfg.Nodes[i].Name == cfg.CurrentNode {
			cfg.CurrentNode = cfg.Nodes[i].ID
			return
		}
	}
}

func uniqueNodeID(nodes []Node, currentIndex int) string {
	for {
		id := generateNodeID()
		duplicate := false
		for i := range nodes {
			if i == currentIndex {
				continue
			}
			if nodes[i].ID == id {
				duplicate = true
				break
			}
		}
		if !duplicate {
			return id
		}
	}
}

func hasNodeID(nodes []Node, id string) bool {
	for i := range nodes {
		if nodes[i].ID == id {
			return true
		}
	}
	return false
}

func generateNodeID() string {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("node-%d-%d", os.Getpid(), time.Now().UnixNano())
	}
	return "node-" + hex.EncodeToString(buf)
}
