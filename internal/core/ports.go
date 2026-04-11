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
	Disconnect(ctx context.Context, nodeID string) error
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
	VerifyMQTTCredentials(username, password string) bool
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
