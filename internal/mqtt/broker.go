package mqtt

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"

	"moleAgent_Serv/internal/auth"
	"moleAgent_Serv/internal/core"
)

// EmbeddedBroker embedded MQTT Broker
type EmbeddedBroker struct {
	server    *mqtt.Server
	tcpAddr   string
	wsAddr    string
	authSvc    *auth.AuthService
	rbac       *auth.RBACEngine
	p2pTokens  auth.P2PSignalTokenVerifier
	topicAuthz func(userID, nodeID string) bool
	stopOnce   sync.Once
}

// NewEmbeddedBroker creates an embedded MQTT Broker
func NewEmbeddedBroker(tcpAddr, wsAddr string, authSvc *auth.AuthService, rbac *auth.RBACEngine) *EmbeddedBroker {
	server := mqtt.New(&mqtt.Options{
		InlineClient: true,
	})

	return &EmbeddedBroker{
		server:  server,
		tcpAddr: tcpAddr,
		wsAddr:  wsAddr,
		authSvc: authSvc,
		rbac:    rbac,
	}
}

// Start starts the MQTT Broker.
// mochi-mqtt Serve() is non-blocking, so Start must arrange shutdown explicitly.
func (b *EmbeddedBroker) Start(ctx context.Context) error {
	// Add Auth Hook
	b.server.AddHook(&authHook{authSvc: b.authSvc, p2pTokens: b.p2pTokens}, nil)
	b.server.AddHook(&aclHook{rbac: b.rbac, p2pTokens: b.p2pTokens, nodeAuthz: b.topicAuthz}, nil)

	// TCP listener
	if b.tcpAddr != "" {
		tcp := listeners.NewTCP(listeners.Config{
			ID:      "tcp",
			Address: b.tcpAddr,
		})
		if err := b.server.AddListener(tcp); err != nil {
			return fmt.Errorf("mqtt tcp listen %s: %w", b.tcpAddr, err)
		}
		slog.Info("MQTT TCP listening", "addr", b.tcpAddr)
	}

	// WebSocket listener
	if b.wsAddr != "" {
		ws := listeners.NewWebsocket(listeners.Config{
			ID:      "ws",
			Address: b.wsAddr,
		})
		if err := b.server.AddListener(ws); err != nil {
			return fmt.Errorf("mqtt ws listen %s: %w", b.wsAddr, err)
		}
		slog.Info("MQTT WebSocket listening", "addr", b.wsAddr)
	}

	if err := b.server.Serve(); err != nil {
		return fmt.Errorf("mqtt serve: %w", err)
	}

	go func() {
		<-ctx.Done()
		if err := b.Stop(context.Background()); err != nil {
			slog.Debug("MQTT broker stop after context cancel", "error", err)
		}
		slog.Info("MQTT broker stopped")
	}()

	return nil
}

// Stop stops the Broker
func (b *EmbeddedBroker) Stop(_ context.Context) error {
	var stopErr error
	b.stopOnce.Do(func() {
		stopErr = b.server.Close()
	})
	return stopErr
}

// Publish publishes a message
func (b *EmbeddedBroker) Publish(topic string, payload []byte, retain bool, qos byte) error {
	return b.server.Publish(topic, payload, retain, qos)
}

// Subscribe subscribes to a topic (inline client)
func (b *EmbeddedBroker) Subscribe(topic string, qos byte, cb core.InlineCallback) error {
	return b.server.Subscribe(topic, 0, func(cl *mqtt.Client, sub packets.Subscription, pk packets.Packet) {
		cb(pk.TopicName, pk.Payload)
	})
}

// GetClients returns connected clients
func (b *EmbeddedBroker) GetClients() []core.MQTTClientInfo {
	clients := b.server.Clients.GetAll()
	result := make([]core.MQTTClientInfo, 0, len(clients))
	for _, c := range clients {
		if c.Net.Inline || c.ID == mqtt.InlineClientId {
			continue
		}
		// 从 UserProperties 恢复原始 username
		username := string(c.Properties.Username)
		for _, up := range c.Properties.Props.User {
			if up.Key == "original_username" {
				username = up.Val
				break
			}
		}
		result = append(result, core.MQTTClientInfo{
			ClientID:  c.ID,
			Username:  username,
			Connected: c.Net.Conn != nil,
		})
	}
	return result
}

// GetStats returns Broker statistics
func (b *EmbeddedBroker) GetStats() core.MQTTStats {
	clients := b.server.Clients.GetAll()
	connected := 0
	total := 0
	for _, c := range clients {
		if c.Net.Inline || c.ID == mqtt.InlineClientId {
			continue
		}
		total++
		if c.Net.Conn != nil {
			connected++
		}
	}
	return core.MQTTStats{
		ClientsConnected: connected,
		ClientsTotal:     total,
	}
}

// GetServer returns the underlying server
func (b *EmbeddedBroker) GetServer() *mqtt.Server {
	return b.server
}

// SetP2PSignalTokenVerifier 注入 P2P 信令凭据校验器（Start 前调用；nil = 拒绝 p2p 哨兵）
func (b *EmbeddedBroker) SetP2PSignalTokenVerifier(v auth.P2PSignalTokenVerifier) {
	b.p2pTokens = v
}

// SetTopicAuthorizer 注入节点级 topic 归属仲裁（Start 前调用）：
// userID 是否可访问归属 nodeID 的 topic（/mole/<nodeId>/...）。
// RBAC 只到 ("mqtt", action) 粒度，不看 topic；不注入 = 不做归属隔离。
func (b *EmbeddedBroker) SetTopicAuthorizer(fn func(userID, nodeID string) bool) {
	b.topicAuthz = fn
}

// mqttAuthFailureThreshold/Window/BlockDur 未认证 CONNECT 的失败节流：
// 已知用户名 + 错误密码的每次 CONNECT 都触发一次 bcrypt（cost≥10，~百 ms 级 CPU），
// 无节流时公网攻击者可用极低成本打满服务端 CPU
const (
	mqttAuthFailureThreshold = 5
	mqttAuthFailureWindow    = time.Minute
	mqttAuthFailureBlock     = 5 * time.Minute
)

type authFailEntry struct {
	count    int
	firstAt  time.Time
	blockedUntil time.Time
}

// authHook mochi-mqtt authentication Hook
type authHook struct {
	mqtt.HookBase
	authSvc   *auth.AuthService
	p2pTokens auth.P2PSignalTokenVerifier
	failMu    sync.Mutex
	failures  map[string]*authFailEntry // username → 失败计数（按用户名节流：攻击者固定用已知用户名；合法用户在受攻击期间 MQTT 暂不可用属可接受代价，Web 登录不受影响）
}

func (h *authHook) ID() string {
	return "auth-hook"
}

func (h *authHook) Provides(b byte) bool {
	return b == mqtt.OnConnectAuthenticate
}

func (h *authHook) OnConnectAuthenticate(cl *mqtt.Client, pk packets.Packet) bool {
	username := string(pk.Connect.Username)
	password := string(pk.Connect.Password)

	if username == "" {
		return false
	}

	// P2P 信令哨兵分支：置于 VerifyMQTTCredentials 之前，与普通用户路径隔离。
	// 校验通过后 Username 置为哨兵字符串本身（保持「Username 即身份载体」契约，
	// aclHook 据前缀限定 nat-exchange/*；不注入 userID，不与用户体系相通）。
	if auth.IsP2PSignalUsername(username) {
		tokenID := strings.TrimPrefix(username, auth.P2PSignalUsernamePrefix)
		if h.p2pTokens != nil && h.p2pTokens.VerifyP2PSignalToken(tokenID, password) {
			cl.Properties.Username = []byte(username)
			slog.Info("MQTT p2p signal client authenticated", "clientId", cl.ID, "username", username)
			return true
		}
		slog.Warn("MQTT p2p signal auth failed", "clientId", cl.ID)
		return false
	}

	if h.authThrottled(username) {
		slog.Warn("MQTT auth throttled (too many failures)", "clientId", cl.ID, "username", username)
		return false
	}

	userID, ok := h.authSvc.VerifyMQTTCredentials(username, password)
	if ok {
		h.recordAuthResult(username, true)
		// 将 userID 存入 client.Properties.Username 供 ACL 使用
		cl.Properties.Username = []byte(userID)
		// 保留原始 username 到 Props.UserProperties 供管理接口展示
		cl.Properties.Props.User = append(cl.Properties.Props.User, packets.UserProperty{
			Key: "original_username", Val: username,
		})
		slog.Info("MQTT client authenticated", "clientId", cl.ID, "username", username, "userID", userID)
	} else {
		h.recordAuthResult(username, false)
		slog.Warn("MQTT auth failed", "clientId", cl.ID, "username", username)
	}
	return ok
}

// authThrottled 该用户名近期失败过多，处于封禁窗口
func (h *authHook) authThrottled(username string) bool {
	h.failMu.Lock()
	defer h.failMu.Unlock()
	e, ok := h.failures[username]
	if !ok {
		return false
	}
	return time.Now().Before(e.blockedUntil)
}

// recordAuthResult 记录认证成败，滑动窗口内失败达到阈值则进入封禁
func (h *authHook) recordAuthResult(username string, success bool) {
	h.failMu.Lock()
	defer h.failMu.Unlock()
	if h.failures == nil {
		h.failures = make(map[string]*authFailEntry)
	}
	now := time.Now()
	if success {
		delete(h.failures, username)
		return
	}
	e, ok := h.failures[username]
	if !ok || now.Sub(e.firstAt) > mqttAuthFailureWindow {
		h.failures[username] = &authFailEntry{count: 1, firstAt: now}
		return
	}
	e.count++
	if e.count >= mqttAuthFailureThreshold {
		e.blockedUntil = now.Add(mqttAuthFailureBlock)
		slog.Warn("MQTT auth failures exceeded threshold, blocking username", "username", username, "until", e.blockedUntil)
	}
}

// aclHook ACL Hook with RBAC integration
type aclHook struct {
	mqtt.HookBase
	rbac      *auth.RBACEngine
	p2pTokens auth.P2PSignalTokenVerifier
	nodeAuthz func(userID, nodeID string) bool
}

// nodeTopicID 从 topic 提取节点级作用域 ID：/mole/<nodeId>/... → nodeId
func nodeTopicID(topic string) (string, bool) {
	const prefix = "/mole/"
	if !strings.HasPrefix(topic, prefix) {
		return "", false
	}
	rest := topic[len(prefix):]
	if idx := strings.Index(rest, "/"); idx >= 0 {
		rest = rest[:idx]
	}
	if rest == "" {
		return "", false
	}
	return rest, true
}

func (h *aclHook) ID() string {
	return "acl-hook"
}

func (h *aclHook) Provides(b byte) bool {
	return b == mqtt.OnACLCheck
}

func (h *aclHook) OnACLCheck(cl *mqtt.Client, topic string, write bool) bool {
	userID := string(cl.Properties.Username) // 已被 authHook 替换为 userID 或 p2p 哨兵
	if userID == "" {
		return false
	}

	// P2P 信令哨兵分支：必须置于 rbac == nil 兜底放行之前，否则 RBAC 未配置时
	// P2P 客户端可订阅任意 topic。仅放行 nat-exchange/ 精确前缀（read/write），
	// 通配符（+/#）一律拒绝——防 nat-exchange/# 全域订阅窃取其他配对信令。
	if auth.IsP2PSignalUsername(userID) {
		if strings.ContainsAny(topic, "+#") {
			slog.Warn("MQTT p2p ACL denied (wildcard)", "clientId", cl.ID, "topic", topic)
			return false
		}
		allowed := strings.HasPrefix(topic, "nat-exchange/")
		if !allowed {
			slog.Warn("MQTT p2p ACL denied", "clientId", cl.ID, "topic", topic)
		}
		return allowed
	}

	// Map MQTT operation to RBAC permission
	action := "read" // subscribe = read
	if write {
		action = "write" // publish = write
	}

	if h.rbac == nil {
		return true // fallback: allow if no RBAC engine
	}

	// 管理员（*/* 权限）不受 topic 级约束
	if admin, err := h.rbac.CheckPermission(userID, "*", "*"); err == nil && admin {
		return true
	}

	// 通配符拒绝：#/+ 全域订阅可跨租户窃听所有节点的串口流量与 p2p 信令
	// （p2p 哨兵分支早有同样防御，普通用户分支此前完全未设防）
	if strings.ContainsAny(topic, "+#") {
		slog.Warn("MQTT ACL denied (wildcard)", "clientId", cl.ID, "userID", userID, "topic", topic)
		return false
	}

	// 节点归属：/mole/<nodeId>/... 只允许节点归属者（或未注入仲裁器时维持旧行为）
	if nodeID, ok := nodeTopicID(topic); ok && h.nodeAuthz != nil {
		if !h.nodeAuthz(userID, nodeID) {
			slog.Warn("MQTT ACL denied (node ownership)", "clientId", cl.ID, "userID", userID, "topic", topic)
			return false
		}
	}

	allowed, err := h.rbac.CheckPermission(userID, "mqtt", action)
	if err != nil {
		slog.Warn("MQTT ACL check error", "userID", userID, "error", err)
		return false
	}

	if !allowed {
		slog.Warn("MQTT ACL denied", "clientId", cl.ID, "userID", userID, "topic", topic, "action", action)
	}
	return allowed
}

// GetSubscriptionsInfo returns subscription info for all connected clients
func GetSubscriptionsInfo(server *mqtt.Server) []map[string]any {
	clients := server.Clients.GetAll()
	var result []map[string]any
	for _, cl := range clients {
		subs := cl.State.Subscriptions.GetAll()
		for filter, sub := range subs {
			result = append(result, map[string]any{
				"client_id": cl.ID,
				"topic":     filter,
				"qos":       sub.Qos,
			})
		}
	}
	return result
}
