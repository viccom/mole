package core

import (
	"time"
)

// NodeStatus 节点在线状态
type NodeStatus string

const (
	NodeStatusOnline  NodeStatus = "online"
	NodeStatusOffline NodeStatus = "offline"
)

// Node 代表一个连接到服务端的代理节点
// 语义说明：
//   - 预配置节点：通过 REST API 预先创建，Status=offline，无运行态会话
//   - 在线节点：通过控制端口注册，Status=online，运行态会话存储在 NodeManager 内部
//   - Node 不再直接持有 smux Session，持久化模型与运行态连接已分离
type Node struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Token         string     `json:"token,omitempty"`
	Status        NodeStatus `json:"status"`
	Tunnels       []Tunnel   `json:"tunnels"`
	RemoteAddr    string     `json:"remote_addr,omitempty"`
	ConnectedAt   *time.Time `json:"connected_at,omitempty"`
	LastHeartbeat *time.Time `json:"last_heartbeat,omitempty"`
	OwnerUserID   string     `json:"owner_user_id,omitempty"`   // 归属用户 ID
	AccessTokenID string     `json:"access_token_id,omitempty"` // 接入 Token ID（审计用）
}

// TunnelType 隧道类型
type TunnelType string

const (
	TunnelTypeHTTP  TunnelType = "http"
	TunnelTypeHTTPS TunnelType = "https"
	TunnelTypeTCP   TunnelType = "tcp"
	TunnelTypeUDP   TunnelType = "udp"
)

// Tunnel 隧道配置
type Tunnel struct {
	Name       string     `json:"name"`
	Type       TunnelType `json:"type"`
	Target     string     `json:"target"`                // 后端地址，host:port 格式（如 127.0.0.1:8080）
	Domain     string     `json:"domain,omitempty"`      // HTTP 隧道的域名
	ListenPort int        `json:"listen_port,omitempty"` // TCP/UDP 隧道的监听端口
	Enabled    *bool      `json:"enabled,omitempty"`     // 启用开关，nil/true=启用，false=禁用
}

// IsEnabled 返回隧道是否启用。零值（nil）视为启用，兼容旧数据。
func (t Tunnel) IsEnabled() bool {
	return t.Enabled == nil || *t.Enabled
}

// UserStatus 用户状态
type UserStatus string

const (
	UserStatusActive   UserStatus = "active"
	UserStatusDisabled UserStatus = "disabled"
)

// User 用户模型
type User struct {
	ID        string     `json:"id"`
	Username  string     `json:"username"`
	Status    UserStatus `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// Role 角色模型
type Role struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Permissions []Permission `json:"permissions"`
}

// Permission 权限条目
type Permission struct {
	Resource string `json:"resource"` // users, roles, nodes, tunnels, mqtt, system, accesskey, *
	Action   string `json:"action"`   // read, write, delete, admin, *
}

// Claims JWT 载荷
type Claims struct {
	UserID   string   `json:"user_id"`
	Username string   `json:"username"`
	Roles    []string `json:"roles"`
}

// MQTTClientInfo MQTT 客户端信息
type MQTTClientInfo struct {
	ClientID  string `json:"client_id"`
	Username  string `json:"username"`
	Connected bool   `json:"connected"`
}

// MQTTStats MQTT Broker 统计
type MQTTStats struct {
	ClientsConnected  int   `json:"clients_connected"`
	ClientsTotal      int   `json:"clients_total"`
	Subscriptions     int   `json:"subscriptions"`
	MessagesPublished int64 `json:"messages_published"`
	MessagesReceived  int64 `json:"messages_received"`
}

// TunnelInfo 隧道运行时信息
type TunnelInfo struct {
	Tunnel
	NodeID      string `json:"node_id"`
	Listeners   int    `json:"listeners"`
	Connections int    `json:"connections"`
}

// ApiResponse 统一 API 响应格式
type ApiResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data any    `json:"data,omitempty"`
}

// PaginatedResponse 分页响应
type PaginatedResponse struct {
	Items      any `json:"items"`
	Total      int `json:"total"`
	Page       int `json:"page"`
	PerPage    int `json:"per_page"`
	TotalPages int `json:"total_pages"`
}

// AccessTokenStatus 接入 Token 状态
type AccessTokenStatus string

const (
	AccessTokenActive   AccessTokenStatus = "active"
	AccessTokenDisabled AccessTokenStatus = "disabled"
)

// AccessToken 用户级节点接入凭据
type AccessToken struct {
	ID          string            `json:"id"`
	UserID      string            `json:"user_id"`
	Name        string            `json:"name"`
	TokenPrefix string            `json:"token_prefix"`
	TokenHash   string            `json:"token_hash"`
	Status      AccessTokenStatus `json:"status"`
	LastUsedAt  *time.Time        `json:"last_used_at,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// NodeAccessGrant 节点接入认证结果
type NodeAccessGrant struct {
	UserID        string
	AccessTokenID string
	LegacyGlobal  bool // 是否使用旧全局 token 认证
}
