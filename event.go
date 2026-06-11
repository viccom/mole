package moleAgent_client

import (
	"sync"
	"time"
)

// EventType 事件类型
type EventType string

const (
	EventConnected          EventType = "connected"
	EventDisconnected      EventType = "disconnected"
	EventReconnecting      EventType = "reconnecting"
	EventAuthenticated     EventType = "authenticated"
	EventRegistered        EventType = "registered"
	EventHeartbeatOK       EventType = "heartbeat_ok"
	EventHeartbeatFail     EventType = "heartbeat_fail"
	EventTunnelUpdated     EventType = "tunnel_updated" // 服务端推送
	EventRTT               EventType = "rtt"            // ping RTT 测量结果
	EventTunnelSynced      EventType = "tunnel_synced"  // 客户端发起同步完成
	EventError             EventType = "error"

	// VPN 管理事件
	EventVPNStarted      EventType = "vpn_started"
	EventVPNStopped      EventType = "vpn_stopped"
	EventVPNCrashed      EventType = "vpn_crashed"
	EventVPNRestarting   EventType = "vpn_restarting"
	EventVPNMaxRestarts  EventType = "vpn_max_restarts"

	// ser2mq 事件
	EventSer2MQConnected     EventType = "ser2mq_connected"
	EventSer2MQDisconnected EventType = "ser2mq_disconnected"
	EventSer2MQError       EventType = "ser2mq_error"
)

// Event 事件
type Event struct {
	Type      EventType
	Timestamp time.Time
	Data      map[string]any
}

// EventHandler 事件处理函数
type EventHandler func(Event)

// EventBus 同步事件总线
type EventBus struct {
	mu       sync.RWMutex
	handlers map[EventType][]EventHandler
	wildcard []EventHandler
}

func newEventBus() *EventBus {
	return &EventBus{
		handlers: make(map[EventType][]EventHandler),
	}
}

// On 注册事件处理器。eventType 为空字符串时注册为通配处理器（接收所有事件）
func (eb *EventBus) On(eventType EventType, handler EventHandler) {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	if eventType == "" {
		eb.wildcard = append(eb.wildcard, handler)
		return
	}
	eb.handlers[eventType] = append(eb.handlers[eventType], handler)
}

// Emit 触发事件（同步调用所有处理器）
func (eb *EventBus) Emit(event Event) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	eb.mu.RLock()
	handlers := eb.handlers[event.Type]
	wildcard := eb.wildcard
	eb.mu.RUnlock()

	for _, h := range handlers {
		h(event)
	}
	for _, h := range wildcard {
		h(event)
	}
}
