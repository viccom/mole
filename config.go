package moleAgent_client

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Config 可从文件加载的配置
type Config struct {
	ServerAddr string   `json:"server_addr"`
	Token      string   `json:"token"`
	NodeID     string   `json:"node_id"`
	NodeName   string   `json:"node_name"`
	// NodeIDFile 指定 node.id 持久化文件路径（nodeID 的真相源）。
	// 留空则用默认 ~/.moleAgent-client/node.id；systemd 等 HOME 不稳定的部署建议显式固定。
	NodeIDFile string `json:"node_id_file,omitempty"`
	Transport  string   `json:"transport"` // 传输协议: tcp, ws, kcp
	UseTLS     bool     `json:"tls"`
	Tunnels    []Tunnel `json:"tunnels"`

	// 内置 HTTP 服务（仅 CLI 模式使用）
	BuiltinHTTP string `json:"http_port"`

	// 心跳配置
	HeartbeatInterval time.Duration `json:"heartbeat_interval,omitempty"`
	HeartbeatTimeout  time.Duration `json:"heartbeat_timeout,omitempty"`

	// 重连间隔
	ReconnectInterval time.Duration `json:"reconnect_interval,omitempty"`

	// KCP 配置
	KCP KCPConfig `json:"kcp,omitempty"`
}

// KCPConfig KCP 协议客户端配置
type KCPConfig struct {
	Key          string `json:"key"`
	DataShards   int    `json:"data_shards"`
	ParityShards int    `json:"parity_shards"`
	NoDelay      int    `json:"nodelay"`
	Interval     int    `json:"interval"`
	Resend       int    `json:"resend"`
	NoCongestion int    `json:"no_congestion"`
	SendWindow   int    `json:"send_window"`
	RecvWindow   int    `json:"recv_window"`
}

// LoadConfigFile 从 JSON 文件加载配置
func LoadConfigFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}
	cfg := DefaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}
	return cfg, nil
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		ServerAddr:        "127.0.0.1:9981",
		Transport:         "tcp",
		Token:             "default-node-token-change-me",
		BuiltinHTTP:       "127.0.0.1:59870",
		HeartbeatInterval: 10 * time.Second,
		HeartbeatTimeout:  5 * time.Second,
		ReconnectInterval: 5 * time.Second,
	}
}

// ApplyDefaults 填充零值字段为默认值，生成 node_id 等
func (c *Config) ApplyDefaults() {
	if c.ServerAddr == "" {
		c.ServerAddr = "127.0.0.1:9981"
	}
	if c.Token == "" {
		c.Token = "default-node-token-change-me"
	}
	if c.Transport == "" {
		c.Transport = "tcp"
	}
	if c.NodeID == "" {
		c.NodeID = DefaultNodeID()
	}
	if c.NodeName == "" {
		c.NodeName = c.NodeID
	}
	if c.HeartbeatInterval == 0 {
		c.HeartbeatInterval = 10 * time.Second
	}
	if c.HeartbeatTimeout == 0 {
		c.HeartbeatTimeout = 5 * time.Second
	}
	if c.ReconnectInterval == 0 {
		c.ReconnectInterval = 5 * time.Second
	}
	if c.BuiltinHTTP == "" {
		c.BuiltinHTTP = "127.0.0.1:59870"
	}
}

// Validate 校验配置合法性
func (c *Config) Validate() error {
	if c.ServerAddr == "" {
		return fmt.Errorf("server_addr is required")
	}
	if c.Token == "" {
		return fmt.Errorf("token is required")
	}
	if !ValidateNodeID(c.NodeID) {
		return fmt.Errorf("invalid node_id %q: must be exactly 8 alphanumeric characters starting with a letter", c.NodeID)
	}

	validTransports := map[string]bool{"tcp": true, "ws": true, "kcp": true}
	if !validTransports[c.Transport] {
		return fmt.Errorf("invalid transport %q (must be tcp, ws or kcp)", c.Transport)
	}

	if c.Transport == "kcp" && c.UseTLS {
		return fmt.Errorf("TLS is not applicable to KCP transport; use kcp.key for encryption instead")
	}

	if c.KCP.DataShards+c.KCP.ParityShards > 255 {
		return fmt.Errorf("kcp data_shards(%d) + parity_shards(%d) must not exceed 255", c.KCP.DataShards, c.KCP.ParityShards)
	}
	if c.KCP.DataShards == 0 && c.KCP.ParityShards > 0 {
		return fmt.Errorf("kcp parity_shards > 0 requires data_shards > 0")
	}

	for _, t := range c.Tunnels {
		if err := t.Validate(); err != nil {
			return fmt.Errorf("tunnel %q: %w", t.Name, err)
		}
	}
	return nil
}
