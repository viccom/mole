package ser2mq

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"sync"
	"time"

	"github.com/eclipse/paho.mqtt.golang"
)

// MQTTConfig MQTT 配置
type MQTTConfig struct {
	Broker   string // mqtt://user:pass@host:port
	QoS      byte   // 0/1/2，默认 1
	ClientID string
}

// Validate 校验配置
func (m *MQTTConfig) Validate() error {
	if m.Broker == "" {
		return fmt.Errorf("mqtt broker is required")
	}
	// 解析 broker URL
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

// MQTTMessage MQTT 消息内容（明文）
type MQTTMessage struct {
	NodeID    string `json:"nodeid"`
	Tunnel    string `json:"tunnel"`
	Timestamp int64  `json:"timestamp"`
	Direction string `json:"direction"` // "out" 或 "in"
	Data      string `json:"data"`     // base64 编码的串口原始数据
}

// MQTTClient MQTT 客户端封装
type MQTTClient struct {
	client mqtt.Client
	cfg    MQTTConfig
	nodeID string
	tunnel string
}

// NewMQTTClient 创建 MQTT 客户端
func NewMQTTClient(cfg MQTTConfig, nodeID, tunnel string) (*MQTTClient, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.QoS == 0 {
		cfg.QoS = 1 // 默认 QoS 1
	}

	// 解析 broker URL 获取连接选项
	u, err := url.Parse(cfg.Broker)
	if err != nil {
		return nil, err
	}

	opts := mqtt.NewClientOptions()
	opts.AddBroker(u.Host)

	// 设置客户端 ID
	clientID := cfg.ClientID
	if clientID == "" {
		clientID = fmt.Sprintf("mole-%s-%s", nodeID, tunnel)
	}
	opts.SetClientID(clientID)

	// 设置用户名密码
	if u.User != nil {
		opts.SetUsername(u.User.Username())
		if p, ok := u.User.Password(); ok {
			opts.SetPassword(p)
		}
	}

	// 连接超时
	opts.SetConnectTimeout(10 * time.Second)
	opts.SetKeepAlive(30 * time.Second)
	opts.SetPingTimeout(5 * time.Second)

	// 自动重连
	opts.SetAutoReconnect(true)
	opts.SetMaxReconnectInterval(10 * time.Second)
	opts.SetConnectRetry(true)

	client := mqtt.NewClient(opts)
	return &MQTTClient{
		client: client,
		cfg:    cfg,
		nodeID: nodeID,
		tunnel: tunnel,
	}, nil
}

// Connect 连接 MQTT Broker
func (m *MQTTClient) Connect(ctx context.Context) error {
	token := m.client.Connect()
	if token.WaitTimeout(10 * time.Second) {
		if token.Error() != nil {
			return fmt.Errorf("mqtt connect: %w", token.Error())
		}
		return nil
	}
	return fmt.Errorf("mqtt connect timeout")
}

// Disconnect 断开连接
func (m *MQTTClient) Disconnect() {
	if m.client != nil && m.client.IsConnected() {
		m.client.Disconnect(5000)
	}
}

// IsConnected 检查连接状态
func (m *MQTTClient) IsConnected() bool {
	return m.client != nil && m.client.IsConnected()
}

// SubscribeTopic 返回订阅主题
func (m *MQTTClient) SubscribeTopic() string {
	portName := SanitizePortName(m.tunnel) // 这里 tunnel 实际上是串口端口名
	return fmt.Sprintf("/mole/%s/serial/%s/out", m.nodeID, portName)
}

// PublishTopic 返回发布主题
func (m *MQTTClient) PublishTopic() string {
	portName := SanitizePortName(m.tunnel)
	return fmt.Sprintf("/mole/%s/serial/%s/in", m.nodeID, portName)
}

// Subscribe 订阅消息
func (m *MQTTClient) Subscribe(topic string, handler func([]byte)) error {
	token := m.client.Subscribe(topic, m.cfg.QoS, func(client mqtt.Client, msg mqtt.Message) {
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

// BuildPayload 构建 MQTT 消息 payload
func BuildPayload(nodeID, tunnelName, direction string, serialData []byte) ([]byte, error) {
	msg := MQTTMessage{
		NodeID:    nodeID,
		Tunnel:    tunnelName,
		Timestamp: time.Now().UnixMilli(),
		Direction: direction,
		Data:      base64.StdEncoding.EncodeToString(serialData),
	}
	return json.Marshal(msg)
}

// ParsePayload 解析 MQTT 消息 payload
func ParsePayload(data []byte) (*MQTTMessage, error) {
	var msg MQTTMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("parse mqtt message: %w", err)
	}
	return &msg, nil
}

// DecryptPayload 解析并解密 payload
func DecryptPayload(data []byte, crypto *Crypto) (*MQTTMessage, error) {
	plaintext, err := crypto.Decrypt(data)
	if err != nil {
		return nil, err
	}
	return ParsePayload(plaintext)
}

// ===== MQTT 连接池 =====

// poolEntry 连接池条目
type poolEntry struct {
	client *MQTTClient
	refcnt int
	mu     sync.Mutex
}

// MQTTPool MQTT 连接池
type MQTTPool struct {
	mu    sync.RWMutex
	pools map[string]*poolEntry // key = broker address
}

// 全局 MQTT 连接池
var globalMQTTPool = &MQTTPool{
	pools: make(map[string]*poolEntry),
}

// GetPool 获取全局连接池
func GetPool() *MQTTPool {
	return globalMQTTPool
}

// Get 获取或创建 MQTT 客户端
func (p *MQTTPool) Get(ctx context.Context, cfg MQTTConfig, nodeID, tunnel, port string) (*MQTTClient, error) {
	brokerKey := cfg.Broker

	p.mu.Lock()
	entry, ok := p.pools[brokerKey]
	if ok {
		entry.refcnt++
		p.mu.Unlock()
		return entry.client, nil
	}

	// 创建新连接
	entry = &poolEntry{
		refcnt: 1,
	}
	p.pools[brokerKey] = entry
	p.mu.Unlock()

	// 尝试复用其他连接的 client ID
	client, err := NewMQTTClient(cfg, nodeID, tunnel)
	if err != nil {
		p.mu.Lock()
		delete(p.pools, brokerKey)
		p.mu.Unlock()
		return nil, err
	}

	entry.client = client
	if err := client.Connect(ctx); err != nil {
		p.mu.Lock()
		delete(p.pools, brokerKey)
		p.mu.Unlock()
		return nil, err
	}

	log.Printf("MQTT client connected to %s (tunnel: %s)", brokerKey, tunnel)
	return client, nil
}

// Put 归还连接
func (p *MQTTPool) Put(broker string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	entry, ok := p.pools[broker]
	if !ok {
		return
	}
	entry.refcnt--
	if entry.refcnt <= 0 {
		entry.client.Disconnect()
		delete(p.pools, broker)
		log.Printf("MQTT client disconnected from %s", broker)
	}
}

// Close 关闭连接池
func (p *MQTTPool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for broker, entry := range p.pools {
		entry.client.Disconnect()
		delete(p.pools, broker)
	}
}

// TopicExists 检查 MQTT 主题是否已订阅（防止同一串口重复创建隧道）
// 这个检查需要在 ser2mq handler 中实现，这里只是标记意图
func TopicExists(topic string) bool {
	// 实际检查由 handler 层通过 tunnel 列表实现
	return false
}

// SanitizeBrokerKey 提取 broker 地址作为 pool key
func SanitizeBrokerKey(broker string) string {
	u, err := url.Parse(broker)
	if err != nil {
		return broker
	}
	return u.Scheme + "://" + u.Host
}
