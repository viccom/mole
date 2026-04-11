package core

import (
	"testing"
)

func TestEventBusSubscribeAndPublish(t *testing.T) {
	bus := NewEventBus()
	received := false

	bus.Subscribe("test.event", func(e Event) {
		received = true
		if e.Type != "test.event" {
			t.Errorf("expected type 'test.event', got '%s'", e.Type)
		}
		if e.Data["key"] != "value" {
			t.Error("expected data['key'] = 'value'")
		}
	})

	bus.Publish(Event{
		Type:   "test.event",
		Source: "test",
		Data:   map[string]any{"key": "value"},
	})

	if !received {
		t.Error("event handler was not called")
	}
}

func TestEventBusNoHandler(t *testing.T) {
	bus := NewEventBus()
	// 不注册 handler，发布事件不应 panic
	bus.Publish(Event{Type: "no.handler", Source: "test"})
}

func TestEventBusMultipleHandlers(t *testing.T) {
	bus := NewEventBus()
	count := 0

	bus.Subscribe("multi", func(e Event) { count++ })
	bus.Subscribe("multi", func(e Event) { count++ })

	bus.Publish(Event{Type: "multi", Source: "test"})

	if count != 2 {
		t.Errorf("expected 2 calls, got %d", count)
	}
}

func TestEventConstants(t *testing.T) {
	// 确保事件常量不为空
	events := []string{
		EventNodeConnected,
		EventNodeDisconnected,
		EventNodeRegistered,
		EventNodeHeartbeat,
		EventNodeTimeout,
		EventTunnelStarted,
		EventTunnelStopped,
		EventUserLogin,
		EventUserLoginFailed,
		EventMQTTClientConnected,
		EventMQTTClientDisconnected,
		EventMQTTAuthFailed,
	}
	for _, e := range events {
		if e == "" {
			t.Error("event constant should not be empty")
		}
	}
}
