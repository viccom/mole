package core

import (
	"context"
	"time"
)

// NodeManager 节点管理器接口
type NodeManager interface {
	Add(ctx context.Context, node *Node) error
	Remove(ctx context.Context, nodeID string) error
	Get(ctx context.Context, nodeID string) (*Node, bool)
	GetAll(ctx context.Context) []*Node
	Update(ctx context.Context, nodeID string, fn func(*Node)) error
	Disconnect(ctx context.Context, nodeID string) error
	// RetireNode 统一退出编排（标记离线→移除→关会话）；架构审查 🔴6：
	// 三个节点清理入口（会话结束/心跳超时/管理面断开）复用此唯一入口
	RetireNode(ctx context.Context, nodeID string) bool
}

// NodeSessionProvider 节点会话获取接口（架构审查 🔴5c：api 层不应依赖
// node.ShardedNodeManager 具体类型）。
//
// 返回 any 而非 *smux.Session：core 保持不引入传输库依赖，调用方做一次类型
// 断言。这是「把传输协议对象泄漏限制在单一断言点」的折中；彻底下沉需把
// WS↔smux 流桥接移入 tunnel 包。
type NodeSessionProvider interface {
	NodeManager
	GetSession(ctx context.Context, nodeID string) (any, error)
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
	MoveTunnel(ctx context.Context, fromNodeID, toNodeID string, tunnel Tunnel) (TunnelChangeResult, error)
	RemoveTunnel(ctx context.Context, nodeID string, tunnelName string) (TunnelChangeResult, error)
	ReplaceTunnels(ctx context.Context, nodeID string, tunnels []Tunnel) (TunnelChangeResult, error)
	SyncFromClient(ctx context.Context, nodeID string, tunnels []Tunnel) error
	LoadPersisted(ctx context.Context, nodeID string) ([]Tunnel, error)
	// ActivateClientTunnels 仅激活客户端配置到运行态（不落库）：
	// LoadPersisted 失败时注册流程的兜底，保证网关监听器不缺失
	ActivateClientTunnels(ctx context.Context, nodeID string, tunnels []Tunnel) error
	UpdateNodeRateLimit(ctx context.Context, nodeID string, rl *NodeRateLimit) error
	BatchUpdateRateLimit(ctx context.Context, items []RateLimitItem) ([]TunnelChangeResult, error)
}

// RateLimitItem batch rate limit update entry.
type RateLimitItem struct {
	NodeID     string          `json:"node_id"`
	TunnelName string          `json:"tunnel_name"`
	RateLimit  *TunnelRateLimit `json:"rate_limit"`
}

// InlineCallback MQTT 内联回调
type InlineCallback func(topic string, payload []byte)

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
	// ListAll 全量列举（SEC-01 proof 认证需要跨用户遍历全部候选）
	ListAll() ([]*AccessToken, error)
	Update(token *AccessToken) error
	Delete(id string) error
	// TouchLastUsed 仅更新 last_used_at：必须重读最新记录后合并写入，
	// 禁止用调用方持有的旧快照整记录回写（会把并发禁用/轮换无声回滚）
	TouchLastUsed(id string, ts time.Time) error
}

// NodeAccessAuthenticator 节点接入认证接口。
// 双格式（SEC-01）：AuthenticateNodeToken 接受旧版明文 token（兼容期）；
// AuthenticateNodeProof 接受 challenge-response proof——proof 绑定本次连接的
// 一次性 challenge，明文 token 不再上线
type NodeAccessAuthenticator interface {
	AuthenticateNodeToken(ctx context.Context, rawToken string) (*NodeAccessGrant, error)
	AuthenticateNodeProof(ctx context.Context, proofHex string, challenge []byte) (*NodeAccessGrant, error)
}

// TunnelStatsReader 隧道运行时统计读取接口（解耦 api 层与 tunnel 层）
type TunnelStatsReader interface {
	Get(name string) *TunnelRuntimeStats
	GetAll() map[string]*TunnelRuntimeStats
	Remove(name string)
}

// FeishuBindingRepo 飞书账户绑定仓库接口
type FeishuBindingRepo interface {
	Create(binding *FeishuBinding) error
	GetByOpenID(openID string) (*FeishuBinding, error)
	GetByUserID(userID string) (*FeishuBinding, error)
	DeleteByOpenID(openID string) error
	DeleteByUserID(userID string) error
}

// DingTalkBindingRepo 钉钉账户绑定仓库接口
type DingTalkBindingRepo interface {
	Create(binding *DingTalkBinding) error
	GetByUnionID(unionID string) (*DingTalkBinding, error)
	GetByUserID(userID string) (*DingTalkBinding, error)
	DeleteByUnionID(unionID string) error
	DeleteByUserID(userID string) error
}

// P2PSignalTokenRepo P2P 信令 Token 仓库接口（与用户 access_token 存储模式对齐）
type P2PSignalTokenRepo interface {
	Upsert(token *P2PSignalToken) error // 按 TokenID 建或替换（维护 secret/owner 双索引）
	GetByID(id string) (*P2PSignalToken, error)
	FindByOwner(nodeID, tunnelName string) (*P2PSignalToken, error)
	ListAll() ([]*P2PSignalToken, error)
	Delete(id string) error
}
