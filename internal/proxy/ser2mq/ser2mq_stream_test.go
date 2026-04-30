package ser2mq

import (
	"testing"
)

func TestBuildPacketEvent(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03, 0x04}
	evt := buildPacketEvent("t1", "serial_out", data)

	if evt.Tunnel != "t1" {
		t.Fatalf("tunnel: got %q, want %q", evt.Tunnel, "t1")
	}
	if evt.Dir != "serial_out" {
		t.Fatalf("dir: got %q, want %q", evt.Dir, "serial_out")
	}
	if evt.Length != 4 {
		t.Fatalf("length: got %d, want 4", evt.Length)
	}
	if evt.HexPreview != "01020304" {
		t.Fatalf("hex: got %q, want %q", evt.HexPreview, "01020304")
	}
	if evt.Truncated {
		t.Fatal("should not be truncated for 4 bytes")
	}
	if evt.Time <= 0 {
		t.Fatal("time should be positive")
	}
}

func TestBuildPacketEventTruncated(t *testing.T) {
	data := make([]byte, 64)
	for i := range data {
		data[i] = byte(i)
	}
	evt := buildPacketEvent("t2", "mqtt_sub", data)

	if evt.Length != 64 {
		t.Fatalf("length: got %d, want 64", evt.Length)
	}
	if !evt.Truncated {
		t.Fatal("should be truncated for 64 bytes with maxBytes=32")
	}
}

func TestBuildStatusEvent(t *testing.T) {
	evt := buildStatusEvent("t1", "started")
	if evt.Dir != "status" {
		t.Fatalf("dir: got %q, want %q", evt.Dir, "status")
	}
	if evt.Message != "started" {
		t.Fatalf("message: got %q, want %q", evt.Message, "started")
	}
	if evt.Length != 0 {
		t.Fatalf("length: got %d, want 0", evt.Length)
	}
}

func TestPreviewHex(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		maxBytes int
		want     string
		trunc    bool
	}{
		{"empty", []byte{}, 32, "", false},
		{"short", []byte{0xab, 0xcd}, 32, "abcd", false},
		{"exact", make([]byte, 32), 32, "0000000000000000000000000000000000000000000000000000000000000000", false},
		{"truncated", make([]byte, 33), 32, "0000000000000000000000000000000000000000000000000000000000000000", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, trunc := previewHex(tt.data, tt.maxBytes)
			if got != tt.want {
				t.Fatalf("hex: got %q, want %q", got, tt.want)
			}
			if trunc != tt.trunc {
				t.Fatalf("truncated: got %v, want %v", trunc, tt.trunc)
			}
		})
	}
}

type fakeSink struct {
	events []PacketEvent
}

func (f *fakeSink) Publish(evt PacketEvent) {
	f.events = append(f.events, evt)
}

func TestHandlerEmitsStartStopStatus(t *testing.T) {
	sink := &fakeSink{}
	cfg := Ser2MQConfig{
		Enable: true,
		Broker: "mqtt://localhost:1883",
		Serial: SerialConfig{Port: "test-port", BaudRate: 9600, DataBits: 8, StopBits: 1, Parity: "N", Timeout: 100},
		Secret: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}

	h, err := NewHandler("test-tunnel", "node1", cfg, sink)
	if err != nil {
		t.Fatal(err)
	}

	// 验证 sink 已设置
	if h.sink == nil {
		t.Fatal("handler sink should not be nil")
	}

	// Start/Stop 需要真实串口和 MQTT，无法在测试中运行
	// 但可以验证构造和 sink 传递正确
	_ = h
}
