package core

import (
	"encoding/json"
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
	RateLimit      *NodeRateLimit `json:"rate_limit,omitempty"`        // per-node rate limit overrides

	// 运行态字段（不持久化，客户端上报）
	SysInfo        *SysInfo              `json:"sysinfo,omitempty"`
	ClientStatuses []ClientTunnelStatus  `json:"client_statuses,omitempty"`
	RTT            int64                 `json:"rtt,omitempty"` // 毫秒
}

// SysInfo 客户端上报的系统信息（不持久化）
type SysInfo struct {
	OS           string `json:"os,omitempty"`
	Hostname     string `json:"hostname,omitempty"`
	Uptime       int64  `json:"uptime_seconds,omitempty"`
	GoVersion    string `json:"go_version,omitempty"`
	AgentVersion string `json:"agent_version,omitempty"`
	NumCPU       int    `json:"num_cpu,omitempty"`
	MemTotalMB   int64  `json:"mem_total_mb,omitempty"`
	MemUsedMB    int64  `json:"mem_used_mb,omitempty"`
}

// ClientTunnelStatus 客户端上报的隧道状态（不持久化）
type ClientTunnelStatus struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	Running       bool   `json:"running"`
	Connected     bool   `json:"connected,omitempty"`
	SerialOpen    bool   `json:"serial_open,omitempty"`
	MQTTConnected bool   `json:"mqtt_connected,omitempty"`
	Clients       int    `json:"clients,omitempty"`
	PID           int    `json:"pid,omitempty"`
	UptimeSeconds int64  `json:"uptime_seconds,omitempty"`
	BytesIn       uint64 `json:"bytes_in,omitempty"`
	BytesOut      uint64 `json:"bytes_out,omitempty"`
	Error         string `json:"error,omitempty"`
}

// TunnelType 隧道类型
type TunnelType string

const (
	TunnelTypeHTTP  TunnelType = "http"
	TunnelTypeHTTPS TunnelType = "https"
	TunnelTypeTCP   TunnelType = "tcp"
	TunnelTypeUDP   TunnelType = "udp"
	TunnelTypeWebSSH TunnelType = "webssh"
	TunnelTypeP2P   TunnelType = "p2p"
)

// Tunnel 隧道配置
type Tunnel struct {
	Name       string          `json:"name"`
	Type       TunnelType     `json:"type"`
	Target     string          `json:"target"`                // 后端地址，host:port 格式（如 127.0.0.1:8080）
	Domain     string          `json:"domain,omitempty"`      // HTTP 隧道的域名
	ListenPort int            `json:"listen_port,omitempty"` // TCP/UDP 隧道的监听端口
	Enabled    *bool           `json:"enabled,omitempty"`     // 启用开关，nil/true=启用，false=禁用
	Para       json.RawMessage `json:"para,omitempty"`        // 扩展配置（ser2mq/vpn-manager 等）
	RateLimit  *TunnelRateLimit `json:"rate_limit,omitempty"` // per-tunnel rate limit overrides
}

// TunnelRateLimit per-tunnel rate limit overrides.
type TunnelRateLimit struct {
	MaxConns      int   `json:"max_conns,omitempty"`      // override max_conns_per_tunnel
	MaxBandwidth int64 `json:"max_bandwidth,omitempty"` // override bandwidth limit (bytes/sec)
}

// NodeRateLimit per-node rate limit overrides.
type NodeRateLimit struct {
	MaxConns int `json:"max_conns,omitempty"` // override max_conns_per_node
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

// TunnelRuntimeStats 单条隧道的运行时统计
type TunnelRuntimeStats struct {
	BytesIn      int64  `json:"bytes_in"`
	BytesOut     int64  `json:"bytes_out"`
	TotalConns   int64  `json:"total_connections"`
	ActiveConns  int64  `json:"active_connections"`
	LastActivity string `json:"last_activity,omitempty"`
}

// FeishuBinding 飞书账户绑定关系
type FeishuBinding struct {
	OpenID     string    `json:"open_id"`
	UserID     string    `json:"user_id"`
	FeishuName string    `json:"feishu_name"`
	AvatarURL  string    `json:"avatar_url,omitempty"`
	BoundAt    time.Time `json:"bound_at"`
}

// DingTalkBinding 钉钉账户绑定关系
type DingTalkBinding struct {
	UnionID    string    `json:"union_id"`
	UserID     string    `json:"user_id"`
	DingName   string    `json:"ding_name"`
	AvatarURL  string    `json:"avatar_url,omitempty"`
	BoundAt    time.Time `json:"bound_at"`
}

// P2PSignalToken P2P 信令凭据（broker nat-exchange/* 专用，与用户体系彻底隔离）。
// 仅存 sha256 hash；明文 secret 仅在签发响应中出现一次，不可二次拉取——
// 因此「幂等复用」落地为 tokenID 稳定 + secret 轮换（客户端每次连接前重拉）。
type P2PSignalToken struct {
	TokenID    string    `json:"token_id"`
	SecretHash string    `json:"secret_hash"` // sha256 hex(secret)
	NodeID     string    `json:"node_id"`
	TunnelName string    `json:"tunnel_name"`
	ExpiresAt  time.Time `json:"expires_at"`
	CreatedAt  time.Time `json:"created_at"`
}
