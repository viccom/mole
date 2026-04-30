package ser2mq

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"time"

	"github.com/eclipse/paho.mqtt.golang"
)

// MQTTConfig MQTT 连接配置
type MQTTConfig struct {
	Broker   string // mqtt://user:pass@host:port
	QoS      byte   // 0/1/2
	ClientID string
}

// Validate 校验配置
func (m *MQTTConfig) Validate() error {
	if m.Broker == "" {
		return fmt.Errorf("mqtt broker is required")
	}
	u, err := url.Parse(m.Broker)
	if err != nil {
		return fmt.Errorf("invalid broker URL: %w", err)
	}
	if u.Scheme != "mqtt" && u.Scheme != "tcp" {
		return fmt.Errorf("unsupported scheme: %s (expected mqtt/tcp)", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("broker host is required")
	}
	return nil
}

// MQTTMessage 与 mole-cgui 兼容的消息格式
type MQTTMessage struct {
	NodeID    string `json:"nodeid"`
	Tunnel    string `json:"tunnel"`
	Timestamp int64  `json:"timestamp"`
	Direction string `json:"direction"` // "out" 或 "in"
	Data      string `json:"data"`      // base64 编码的串口原始数据
}

// MQTTClient MQTT 客户端封装（每隧道独立连接）
type MQTTClient struct {
	client   mqtt.Client
	cfg      MQTTConfig
	outTopic string
	inTopic  string
}

// NewMQTTClient 创建并连接 MQTT 客户端
func NewMQTTClient(ctx context.Context, cfg MQTTConfig, nodeID, tunnel string) (*MQTTClient, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.QoS == 0 {
		cfg.QoS = 1
	}

	u, err := url.Parse(cfg.Broker)
	if err != nil {
		return nil, err
	}

	portName := SanitizePortName(tunnel)

	opts := mqtt.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("tcp://%s", u.Host))

	clientID := cfg.ClientID
	if clientID == "" {
		clientID = fmt.Sprintf("mole-%s-%s", nodeID, portName)
	}
	opts.SetClientID(clientID)

	if u.User != nil {
		opts.SetUsername(u.User.Username())
		if p, ok := u.User.Password(); ok {
			opts.SetPassword(p)
		}
	}

	opts.SetConnectTimeout(10 * time.Second)
	opts.SetKeepAlive(30 * time.Second)
	opts.SetPingTimeout(5 * time.Second)
	opts.SetAutoReconnect(true)
	opts.SetMaxReconnectInterval(10 * time.Second)
	opts.SetConnectRetry(true)

	opts.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		log.Printf("mqtt [%s] connection lost: %v", tunnel, err)
	})

	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.WaitTimeout(10 * time.Second) {
		if token.Error() != nil {
			return nil, fmt.Errorf("mqtt connect: %w", token.Error())
		}
	} else {
		return nil, fmt.Errorf("mqtt connect timeout")
	}

	return &MQTTClient{
		client:   client,
		cfg:      cfg,
		outTopic: fmt.Sprintf("/mole/%s/serial/%s/out", nodeID, portName),
		inTopic:  fmt.Sprintf("/mole/%s/serial/%s/in", nodeID, portName),
	}, nil
}

// Disconnect 断开连接
func (m *MQTTClient) Disconnect() {
	if m.client != nil && m.client.IsConnected() {
		m.client.Disconnect(1000)
	}
}

// IsConnected 检查连接状态
func (m *MQTTClient) IsConnected() bool {
	return m.client != nil && m.client.IsConnected()
}

// OutTopic 串口输出主题：从串口读取的数据发布到 /out
func (m *MQTTClient) OutTopic() string { return m.outTopic }

// InTopic 串口输入主题：从 MQTT 接收的数据写入串口
func (m *MQTTClient) InTopic() string { return m.inTopic }

// Subscribe 订阅消息
func (m *MQTTClient) Subscribe(topic string, handler func([]byte)) error {
	token := m.client.Subscribe(topic, m.cfg.QoS, func(_ mqtt.Client, msg mqtt.Message) {
		handler(msg.Payload())
		msg.Ack()
	})
	if token.WaitTimeout(5 * time.Second) {
		if token.Error() != nil {
			return fmt.Errorf("mqtt subscribe %s: %w", topic, token.Error())
		}
		return nil
	}
	return fmt.Errorf("mqtt subscribe timeout")
}

// Publish 发布消息
func (m *MQTTClient) Publish(topic string, payload []byte) error {
	token := m.client.Publish(topic, m.cfg.QoS, false, payload)
	if token.WaitTimeout(5 * time.Second) {
		return token.Error()
	}
	return fmt.Errorf("mqtt publish timeout")
}

// BuildMQTTMessage 构建 MQTTMessage（串口→MQTT 方向）
func BuildMQTTMessage(nodeID, tunnelName string, serialData []byte) ([]byte, error) {
	msg := MQTTMessage{
		NodeID:    nodeID,
		Tunnel:    tunnelName,
		Timestamp: time.Now().UnixMilli(),
		Direction: "out",
		Data:      base64.StdEncoding.EncodeToString(serialData),
	}
	return json.Marshal(msg)
}
