package ser2mq

import (
	"context"
	"testing"
)

func TestOnTunnelUpdateRestartsChangedTunnel(t *testing.T) {
	manager := NewManager(context.Background(), "node001")

	started := 0
	manager.newHandler = func(name, nodeID string, cfg Ser2MQConfig) (*Ser2MQHandler, error) {
		return &Ser2MQHandler{name: name, nodeID: nodeID, cfg: cfg}, nil
	}
	manager.startHandler = func(h *Ser2MQHandler, ctx context.Context) error {
		started++
		h.running = true
		return nil
	}

	original := &Ser2MQHandler{
		name:    "serial-a",
		nodeID:  "node001",
		cfg:     Ser2MQConfig{Enable: true, Broker: "mqtt://old", Serial: SerialConfig{Port: "COM3"}, Secret: testSecret()},
		running: true,
	}
	manager.tunnels["serial-a"] = original

	manager.OnTunnelUpdate(map[string]Ser2MQConfig{
		"serial-a": {Enable: true, Broker: "mqtt://new", Serial: SerialConfig{Port: "COM3"}, Secret: testSecret()},
	})

	got := manager.tunnels["serial-a"]
	if got == original {
		t.Fatal("OnTunnelUpdate() should replace handler when config changes")
	}
	if got.cfg.Broker != "mqtt://new" {
		t.Fatalf("OnTunnelUpdate() broker = %q, want mqtt://new", got.cfg.Broker)
	}
	if started != 1 {
		t.Fatalf("OnTunnelUpdate() should restart changed tunnel exactly once, got %d", started)
	}
}

func testSecret() string {
	return "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
}
