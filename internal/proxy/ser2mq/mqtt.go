package ser2mq

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
		return brokerError(m.Broker, err)
	}
	if u.Scheme != "mqtt" && u.Scheme != "tcp" {
		return fmt.Errorf("unsupported scheme: %s (expected mqtt/tcp)", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("broker host is required")
	}
	return nil
}

// RedactedBroker 返回 broker URL 的脱敏形式：去掉 userinfo，保留 scheme://host[:port]。
//
// 使用场景：任何可能进入日志或错误信息的路径。文档形态
// mqtt://user:pass@host:port 的 userinfo 是凭据必须剥离；host:port 是
// 排查连接问题所必需的信息，尽量保留。任何情况下不回退原串。
//
// 复核轮确立的两条规则（均有对抗用例钉住）：
//   - 含 '@' 一律走手工剥离，不信任 url.Parse——密码含 '/' 时 authority
//     提前终止，url.Parse 会把 user:pa 误当 host「成功」解析
//     （Host="user:pa:" 或 Host="alice:1883"），伪成功无法靠 host 形态
//     校验完全识破。按最后一个 '@' 剥离对「密码含 @ 与 /」都正确；
//     代价是路径含 '@' 的显示错误（broker 连接只用 host，路径无意义）
//   - 手工剥离结果仍须过 hostPortValid——无 '@' 且解析失败的残片
//     （如 "mqtt://admin:S3cr3t"，操作员想写凭据但漏了 @host）不可信
func RedactedBroker(raw string) string {
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "@") {
		// 无 '@' ⇒ 按 URL 语法不可能存在 userinfo，可信任解析结果
		if u, err := url.Parse(raw); err == nil && u.Host != "" && hostPortValid(u.Host) {
			return u.Scheme + "://" + u.Host
		}
	}
	scheme := schemePrefix(raw)
	rest := raw[len(scheme):]
	if i := strings.LastIndex(rest, "@"); i >= 0 {
		rest = rest[i+1:]
	}
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	if rest == "" || !hostPortValid(rest) {
		return "<redacted>"
	}
	return scheme + rest
}

// schemePrefix 提取 "scheme://" 前缀；前缀段必须符合 RFC 3986 scheme
// 字符集（字母开头，[a-zA-Z0-9+.-]），否则视为无 scheme——否则密码含
// "://" 时（"user:pa://ss@host"）会从密码中切出含 ':' 的伪 scheme
// 前缀，把用户名带进输出
func schemePrefix(raw string) string {
	i := strings.Index(raw, "://")
	if i <= 0 {
		return ""
	}
	for j := 0; j < i; j++ {
		c := raw[j]
		isAlnum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if j == 0 && !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
			return ""
		}
		if !isAlnum && c != '+' && c != '-' && c != '.' {
			return ""
		}
	}
	return raw[:i+3]
}

// hostPortValid 判定 host[:port] 形态合法（端口缺省或 0-65535 数字）。
// 用于识破 url.Parse 的伪成功与手工剥离后的不可信残片。
// 注意与 tunnel.go validateHostPort 的差异（此处端口可缺省）是有意的：
// broker 允许无端口形态，二者不应合并
func hostPortValid(hostport string) bool {
	_, port, err := net.SplitHostPort(hostport)
	if err != nil {
		// 无端口形态（"broker.example.com"）合法；但含裸 ':' 的残片不合法
		return !strings.Contains(hostport, ":")
	}
	if port == "" {
		// "host:"（尾随冒号空端口）视为残片——url.Parse 对
		// "mqtt://user:/path" 会给出 Host="user:"，不能当合法 host 展示
		return !strings.HasSuffix(hostport, ":")
	}
	n, err := strconv.Atoi(port)
	return err == nil && n >= 0 && n <= 65535
}

// brokerError 构造不含凭据的 broker 解析错误。
//
// 必要性：url.Parse 返回的 *url.Error 其 Error() 形如
// `parse "mqtt://user:pass@host:1883": <cause>`——原样上抛会让凭据随
// 错误信息进入日志（ser2mq manager 会 log.Printf 这些 err）。
// 这里只保留内层 cause，并附上脱敏后的 URL。
//
// 残余风险：cause 可能是 EscapeError，其文本含至多 3 字节的问题片段
// （如密码中出现裸 %）。可接受——相比完整凭据泄漏，这是数量级改善，
// 且完全丢弃 cause 会损失可诊断性。
func brokerError(raw string, err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("invalid broker URL %s: %w", RedactedBroker(raw), ue.Err)
	}
	return fmt.Errorf("invalid broker URL %s: %w", RedactedBroker(raw), err)
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

	mu          sync.Mutex
	onReconnect func() // 重连成功后的补订阅回调
	connected   atomic.Bool
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
		// 不直接 return err：*url.Error 的文本内嵌原始 URL（含 user:pass@），
		// 上抛后会经 manager 的 log.Printf 落盘
		return nil, brokerError(cfg.Broker, err)
	}

	portName := SanitizePortName(tunnel)

	m := &MQTTClient{
		cfg:      cfg,
		outTopic: fmt.Sprintf("/mole/%s/serial/%s/out", nodeID, portName),
		inTopic:  fmt.Sprintf("/mole/%s/serial/%s/in", nodeID, portName),
	}

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

	// CleanSession=true 下自动重连会丢失 broker 侧订阅，重连成功必须补订阅
	opts.OnConnect = func(_ mqtt.Client) {
		if !m.connected.Swap(true) {
			return // 首连：由调用方显式订阅
		}
		m.mu.Lock()
		fn := m.onReconnect
		m.mu.Unlock()
		if fn != nil {
			go fn()
		}
	}

	client := mqtt.NewClient(opts)
	m.client = client
	if token := client.Connect(); token.WaitTimeout(10 * time.Second) {
		if token.Error() != nil {
			client.Disconnect(100)
			return nil, fmt.Errorf("mqtt connect: %w", token.Error())
		}
	} else {
		// 超时必须终止半途的 client，否则 ConnectRetry 的重试循环永久残留，
		// 且每次失败重试都会再泄漏一个
		client.Disconnect(100)
		return nil, fmt.Errorf("mqtt connect timeout")
	}

	return m, nil
}

// SetOnReconnect 注册重连成功后的回调（用于补订阅）
func (m *MQTTClient) SetOnReconnect(fn func()) {
	m.mu.Lock()
	m.onReconnect = fn
	m.mu.Unlock()
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
