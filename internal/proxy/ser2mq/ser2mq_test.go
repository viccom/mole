package ser2mq

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func TestSerialConfigJSONTags(t *testing.T) {
	input := `{"port":"COM1","baudrate":9600,"databits":8,"stopbits":1.0,"parity":"N","timeout":3000}`
	var cfg SerialConfig
	if err := json.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.Port != "COM1" {
		t.Errorf("Port = %q, want COM1", cfg.Port)
	}
	if cfg.BaudRate != 9600 {
		t.Errorf("BaudRate = %d, want 9600", cfg.BaudRate)
	}
	if cfg.Timeout != 3000 {
		t.Errorf("Timeout = %d, want 3000", cfg.Timeout)
	}
}

func TestSerialConfigRoundtrip(t *testing.T) {
	cfg := SerialConfig{
		Port:     "/dev/ttyUSB0",
		BaudRate: 115200,
		DataBits: 8,
		StopBits: 1.0,
		Parity:   "N",
		Timeout:  50,
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var cfg2 SerialConfig
	if err := json.Unmarshal(data, &cfg2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg2.Port != cfg.Port {
		t.Errorf("Port = %q, want %q", cfg2.Port, cfg.Port)
	}
	if cfg2.BaudRate != cfg.BaudRate {
		t.Errorf("BaudRate = %d, want %d", cfg2.BaudRate, cfg.BaudRate)
	}
}

func TestSer2MQConfigJSONTags(t *testing.T) {
	input := `{
		"enable": true,
		"broker": "mqtt://user:pass@localhost:1883",
		"serial": {"port":"/dev/ttyUSB0","baudrate":9600,"databits":8,"stopbits":1.0,"parity":"N","timeout":3000},
		"secret": "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f",
		"qos": 2
	}`
	var cfg Ser2MQConfig
	if err := json.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !cfg.Enable {
		t.Error("Enable should be true")
	}
	if cfg.QoS != 2 {
		t.Errorf("QoS = %d, want 2", cfg.QoS)
	}
	if cfg.Serial.Port != "/dev/ttyUSB0" {
		t.Errorf("Serial.Port = %q, want /dev/ttyUSB0", cfg.Serial.Port)
	}
}

func TestSanitizePortName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/dev/ttyUSB0", "ttyUSB0"},
		{`\\.\COM10`, "COM10"},
		{"COM1", "COM1"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := SanitizePortName(tt.input)
			if result != tt.want {
				t.Errorf("SanitizePortName(%q) = %q, want %q", tt.input, result, tt.want)
			}
		})
	}
}

func TestCryptoRoundtrip(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}
	crypto, err := NewCrypto(key)
	if err != nil {
		t.Fatalf("NewCrypto failed: %v", err)
	}
	plaintext := []byte("hello serial world 12345")
	encrypted, err := crypto.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	if encrypted[0] != 0x01 {
		t.Errorf("version byte = 0x%02x, want 0x01", encrypted[0])
	}
	decrypted, err := crypto.Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Errorf("roundtrip failed: got %q, want %q", string(decrypted), string(plaintext))
	}
}

func TestBuildMQTTMessage(t *testing.T) {
	data := []byte("test serial data")
	msgBytes, err := BuildMQTTMessage("node001", "ttyUSB0", data)
	if err != nil {
		t.Fatalf("BuildMQTTMessage failed: %v", err)
	}

	var msg MQTTMessage
	if err := json.Unmarshal(msgBytes, &msg); err != nil {
		t.Fatalf("unmarshal MQTTMessage: %v", err)
	}
	if msg.NodeID != "node001" {
		t.Errorf("NodeID = %q, want node001", msg.NodeID)
	}
	if msg.Tunnel != "ttyUSB0" {
		t.Errorf("Tunnel = %q, want ttyUSB0", msg.Tunnel)
	}
	if msg.Direction != "out" {
		t.Errorf("Direction = %q, want out", msg.Direction)
	}
	if msg.Timestamp <= 0 {
		t.Error("Timestamp should be positive")
	}

	decoded, err := base64.StdEncoding.DecodeString(msg.Data)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if string(decoded) != string(data) {
		t.Errorf("data roundtrip: got %q, want %q", string(decoded), string(data))
	}
}

// TestMQTTMessageCompatibility 验证与 mole-cgui 消息格式兼容
func TestMQTTMessageCompatibility(t *testing.T) {
	// 模拟 mole-cgui 发送的消息格式
	cguiMsg := `{"nodeid":"abc12345","tunnel":"COM3","timestamp":1713761234567,"direction":"out","data":"aGVsbG8="}`
	var msg MQTTMessage
	if err := json.Unmarshal([]byte(cguiMsg), &msg); err != nil {
		t.Fatalf("parse cgui message: %v", err)
	}
	if msg.NodeID != "abc12345" {
		t.Errorf("NodeID = %q, want abc12345", msg.NodeID)
	}
	decoded, _ := base64.StdEncoding.DecodeString(msg.Data)
	if string(decoded) != "hello" {
		t.Errorf("Data = %q, want hello", string(decoded))
	}
}

// TestEncryptedMQTTMessageRoundtrip 模拟完整的加密消息流程
func TestEncryptedMQTTMessageRoundtrip(t *testing.T) {
	key, _ := GenerateKey()
	crypto, _ := NewCrypto(key)

	serialData := []byte("serial payload test")
	msgBytes, _ := BuildMQTTMessage("testnode1", "ttyS0", serialData)
	encrypted, _ := crypto.Encrypt(msgBytes)

	// 模拟接收端解密
	plaintext, err := crypto.Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	var msg MQTTMessage
	if err := json.Unmarshal(plaintext, &msg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	decoded, _ := base64.StdEncoding.DecodeString(msg.Data)
	if string(decoded) != string(serialData) {
		t.Errorf("data mismatch: got %q, want %q", string(decoded), string(serialData))
	}

	// 时间戳应在 30s 窗口内
	now := time.Now().UnixMilli()
	if msg.Timestamp < now-30000 || msg.Timestamp > now+30000 {
		t.Errorf("timestamp out of window: %d (now: %d)", msg.Timestamp, now)
	}
}

func TestMQTTTopicDirections(t *testing.T) {
	client := &MQTTClient{
		outTopic: "/mole/testnode/serial/ttyUSB0/out",
		inTopic:  "/mole/testnode/serial/ttyUSB0/in",
	}

	if client.OutTopic() != "/mole/testnode/serial/ttyUSB0/out" {
		t.Errorf("OutTopic = %q, wrong", client.OutTopic())
	}
	if client.InTopic() != "/mole/testnode/serial/ttyUSB0/in" {
		t.Errorf("InTopic = %q, wrong", client.InTopic())
	}
}

func TestMQTTTopicWindowsPort(t *testing.T) {
	client := &MQTTClient{
		outTopic: "/mole/testnode/serial/COM10/out",
		inTopic:  "/mole/testnode/serial/COM10/in",
	}

	if client.OutTopic() != "/mole/testnode/serial/COM10/out" {
		t.Errorf("OutTopic = %q, wrong", client.OutTopic())
	}
}
