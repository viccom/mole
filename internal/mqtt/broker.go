package mqtt

import (
	"context"
	"fmt"
	"log/slog"

	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"

	"moleAgent_Serv/internal/auth"
	"moleAgent_Serv/internal/core"
)

// EmbeddedBroker embedded MQTT Broker
type EmbeddedBroker struct {
	server  *mqtt.Server
	tcpAddr string
	wsAddr  string
	authSvc *auth.AuthService
	rbac    *auth.RBACEngine
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

// Start starts the MQTT Broker (non-blocking: Serve runs in background goroutine)
func (b *EmbeddedBroker) Start(ctx context.Context) error {
	// Add Auth Hook
	b.server.AddHook(&authHook{authSvc: b.authSvc}, nil)
	b.server.AddHook(&aclHook{rbac: b.rbac}, nil)

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

	// Serve in background goroutine, return errors via channel
	serveErr := make(chan error, 1)
	go func() {
		if err := b.server.Serve(); err != nil {
			serveErr <- fmt.Errorf("mqtt serve: %w", err)
		}
		close(serveErr)
	}()

	// Watch for context cancellation and serve errors
	go func() {
		select {
		case <-ctx.Done():
			b.server.Close()
			slog.Info("MQTT broker stopped")
		case err := <-serveErr:
			if err != nil {
				slog.Error("MQTT broker serve error", "error", err)
			}
		}
	}()

	return nil
}

// Stop stops the Broker
func (b *EmbeddedBroker) Stop(_ context.Context) error {
	return b.server.Close()
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
	for _, c := range clients {
		if c.Net.Conn != nil {
			connected++
		}
	}
	return core.MQTTStats{
		ClientsConnected: connected,
		ClientsTotal:     len(clients),
	}
}

// GetServer returns the underlying server
func (b *EmbeddedBroker) GetServer() *mqtt.Server {
	return b.server
}

// authHook mochi-mqtt authentication Hook
type authHook struct {
	mqtt.HookBase
	authSvc *auth.AuthService
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

	userID, ok := h.authSvc.VerifyMQTTCredentials(username, password)
	if ok {
		// 将 userID 存入 client.Properties.Username 供 ACL 使用
		cl.Properties.Username = []byte(userID)
		// 保留原始 username 到 Props.UserProperties 供管理接口展示
		cl.Properties.Props.User = append(cl.Properties.Props.User, packets.UserProperty{
			Key: "original_username", Val: username,
		})
		slog.Info("MQTT client authenticated", "clientId", cl.ID, "username", username, "userID", userID)
	} else {
		slog.Warn("MQTT auth failed", "clientId", cl.ID, "username", username)
	}
	return ok
}

// aclHook ACL Hook with RBAC integration
type aclHook struct {
	mqtt.HookBase
	rbac *auth.RBACEngine
}

func (h *aclHook) ID() string {
	return "acl-hook"
}

func (h *aclHook) Provides(b byte) bool {
	return b == mqtt.OnACLCheck
}

func (h *aclHook) OnACLCheck(cl *mqtt.Client, topic string, write bool) bool {
	userID := string(cl.Properties.Username) // 已被 authHook 替换为 userID
	if userID == "" {
		return false
	}

	// Map MQTT operation to RBAC permission
	action := "read" // subscribe = read
	if write {
		action = "write" // publish = write
	}

	if h.rbac == nil {
		return true // fallback: allow if no RBAC engine
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
