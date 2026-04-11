package core

import "sync"

// 事件总线使用的事件常量
const (
	EventNodeConnected    = "node.connected"
	EventNodeDisconnected = "node.disconnected"
	EventNodeRegistered   = "node.registered"
	EventNodeHeartbeat    = "node.heartbeat"
	EventNodeTimeout      = "node.timeout"

	EventTunnelStarted = "tunnel.started"
	EventTunnelStopped = "tunnel.stopped"

	EventUserLogin      = "user.login"
	EventUserLoginFailed = "user.login_failed"

	EventMQTTClientConnected    = "mqtt.client_connected"
	EventMQTTClientDisconnected = "mqtt.client_disconnected"
	EventMQTTAuthFailed         = "mqtt.auth_failed"
)

// Event 事件结构
type Event struct {
	Type   string
	Source string
	Data   map[string]any
}

// EventHandler 事件处理函数
type EventHandler func(event Event)

// EventBus 简单事件总线
type EventBus struct {
	mu       sync.RWMutex
	handlers map[string][]EventHandler
}

// NewEventBus 创建事件总线
func NewEventBus() *EventBus {
	return &EventBus{
		handlers: make(map[string][]EventHandler),
	}
}

// Subscribe 订阅事件
func (eb *EventBus) Subscribe(eventType string, handler EventHandler) {
	eb.mu.Lock()
	eb.handlers[eventType] = append(eb.handlers[eventType], handler)
	eb.mu.Unlock()
}

// Publish 发布事件（同步调用）
func (eb *EventBus) Publish(event Event) {
	eb.mu.RLock()
	handlers := eb.handlers[event.Type]
	eb.mu.RUnlock()
	for _, h := range handlers {
		h(event)
	}
}
