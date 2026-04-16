package core

import (
	"context"
)

// NodeManager 节点管理器接口
type NodeManager interface {
	Add(ctx context.Context, node *Node) error
	Remove(ctx context.Context, nodeID string) error
	Get(ctx context.Context, nodeID string) (*Node, bool)
	GetAll(ctx context.Context) []*Node
	Update(ctx context.Context, nodeID string, fn func(*Node)) error
	Disconnect(ctx context.Context, nodeID string) error
}

// TunnelChangeResult 表示一次隧道配置变更在服务端和客户端两侧的落地结果
type TunnelChangeResult struct {
	Status       string
	Persisted    bool
	ClientSynced bool
	Warning      string
}

// TunnelConfigManager 统一处理隧道配置变更、持久化、索引刷新和客户端同步
type TunnelConfigManager interface {
	ApplyTunnel(ctx context.Context, nodeID string, tunnel Tunnel) (TunnelChangeResult, error)
	RemoveTunnel(ctx context.Context, nodeID string, tunnelName string) (TunnelChangeResult, error)
	ReplaceTunnels(ctx context.Context, nodeID string, tunnels []Tunnel) (TunnelChangeResult, error)
	SyncFromClient(ctx context.Context, nodeID string, tunnels []Tunnel) error
	LoadPersisted(ctx context.Context, nodeID string) ([]Tunnel, error)
}

// TunnelManager 隧道管理器接口
type TunnelManager interface {
	StartTCP(ctx context.Context, tunnel Tunnel) error
	StartUDP(ctx context.Context, tunnel Tunnel) error
	RegisterHTTP(ctx context.Context, tunnel Tunnel) error
	Stop(ctx context.Context, tunnelName string) error
	List() []TunnelInfo
}

// MQTTBroker 嵌入式 MQTT Broker 接口
type MQTTBroker interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Publish(topic string, payload []byte, retain bool, qos byte) error
	Subscribe(topic string, qos byte, cb InlineCallback) error
	GetClients() []MQTTClientInfo
	GetStats() MQTTStats
}

// InlineCallback MQTT 内联回调
type InlineCallback func(topic string, payload []byte)

// AuthService 认证服务接口
type AuthService interface {
	Login(ctx context.Context, username, password string) (string, string, error)
	VerifyToken(token string) (*Claims, error)
	RefreshToken(token string) (string, error)
	ChangePassword(ctx context.Context, userID, oldPass, newPass string) error
	VerifyMQTTCredentials(username, password string) (string, bool)
}

// RBACChecker RBAC 权限检查接口
type RBACChecker interface {
	CheckPermission(userID, resource, action string) (bool, error)
	GetUserRoles(userID string) ([]Role, error)
	AssignRole(userID, roleID string) error
	RevokeRole(userID, roleID string) error
}

// UserRepo 用户仓库接口
type UserRepo interface {
	Create(user *User, passwordHash string) error
	GetByID(id string) (*User, error)
	GetByUsername(username string) (*User, error)
	GetAll() ([]*User, error)
	Update(user *User) error
	Delete(id string) error
	SetPasswordHash(id, hash string) error
	GetPasswordHash(id string) (string, error)
}

// RoleRepo 角色仓库接口
type RoleRepo interface {
	Create(role *Role) error
	GetByID(id string) (*Role, error)
	GetAll() ([]*Role, error)
	Update(role *Role) error
	Delete(id string) error
}

// NodeRepo 节点仓库接口
type NodeRepo interface {
	Create(node *Node) error
	GetByID(id string) (*Node, error)
	GetAll() ([]*Node, error)
	Update(node *Node) error
	Delete(id string) error
}

// AccessTokenRepo 接入 Token 仓库接口
type AccessTokenRepo interface {
	Create(token *AccessToken) error
	GetByID(id string) (*AccessToken, error)
	GetByHash(hash string) (*AccessToken, error)
	ListByUser(userID string) ([]*AccessToken, error)
	Update(token *AccessToken) error
	Delete(id string) error
}

// NodeAccessAuthenticator 节点接入认证接口
type NodeAccessAuthenticator interface {
	AuthenticateNodeToken(ctx context.Context, rawToken string) (*NodeAccessGrant, error)
}

// TunnelStatsReader 隧道运行时统计读取接口（解耦 api 层与 tunnel 层）
type TunnelStatsReader interface {
	Get(name string) *TunnelRuntimeStats
	GetAll() map[string]*TunnelRuntimeStats
	Remove(name string)
}
