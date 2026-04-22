package ser2mq

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"sync"
	"time"
)

// Ser2MQConfig ser2mq 隧道配置
type Ser2MQConfig struct {
	Enable  bool          `json:"enable"`
	Broker  string        `json:"broker"`  // mqtt://user:pass@host:port
	Serial  SerialConfig  `json:"serial"`
	Secret  string        `json:"secret"`   // 32字节 hex 字符串
}

// Ser2MQHandler ser2mq 隧道处理器
type Ser2MQHandler struct {
	name      string
	nodeID    string
	cfg       Ser2MQConfig
	crypto    *Crypto
	serial    SerialConn
	mqtt      *MQTTClient
	pool      *MQTTPool

	mu        sync.RWMutex
	running   bool
	ctx       context.Context
	cancel    context.CancelFunc

	// 统计
	bytesIn   uint64
	bytesOut  uint64

	// 防重放
	lastTimestamp int64
}

// NewHandler 创建 ser2mq 处理器
func NewHandler(name, nodeID string, cfg Ser2MQConfig) (*Ser2MQHandler, error) {
	// 解析加密密钥
	crypto, err := NewCrypto(cfg.Secret)
	if err != nil {
		return nil, fmt.Errorf("invalid crypto config: %w", err)
	}

	return &Ser2MQHandler{
		name:   name,
		nodeID: nodeID,
		cfg:    cfg,
		crypto: crypto,
	}, nil
}

// Start 启动 ser2mq 隧道
func (h *Ser2MQHandler) Start(ctx context.Context) error {
	h.mu.Lock()
	if h.running {
		h.mu.Unlock()
		return fmt.Errorf("ser2mq tunnel %s already running", h.name)
	}
	h.ctx, h.cancel = context.WithCancel(ctx)
	h.running = true
	h.mu.Unlock()

	var err error

	// 打开串口
	h.serial, err = OpenSerial(h.cfg.Serial)
	if err != nil {
		h.Stop()
		return fmt.Errorf("open serial: %w", err)
	}

	// 创建 MQTT 客户端
	mqttCfg := MQTTConfig{
		Broker: h.cfg.Broker,
		QoS:    1,
	}
	h.mqtt, err = GetPool().Get(h.ctx, mqttCfg, h.nodeID, h.name, h.cfg.Serial.Port)
	if err != nil {
		h.Stop()
		return fmt.Errorf("connect mqtt: %w", err)
	}

	// 启动数据流
	go h.runSerialToMQTT()
	go h.runMQTTToSerial()

	log.Printf("ser2mq tunnel %s started (broker: %s, serial: %s)", h.name, h.cfg.Broker, h.cfg.Serial.Port)
	return nil
}

// Stop 停止 ser2mq 隧道
func (h *Ser2MQHandler) Stop() {
	h.mu.Lock()
	if !h.running {
		h.mu.Unlock()
		return
	}
	h.running = false
	if h.cancel != nil {
		h.cancel()
	}
	h.mu.Unlock()

	// 关闭串口
	if h.serial != nil {
		h.serial.Close()
		h.serial = nil
	}

	// 归还 MQTT 连接
	if h.mqtt != nil {
		GetPool().Put(h.cfg.Broker)
		h.mqtt = nil
	}

	log.Printf("ser2mq tunnel %s stopped", h.name)
}

// IsRunning 检查是否运行中
func (h *Ser2MQHandler) IsRunning() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.running
}

// Stats 返回统计信息
func (h *Ser2MQHandler) Stats() Ser2MQStats {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return Ser2MQStats{
		Name:       h.name,
		Running:    h.running,
		BytesIn:    h.bytesIn,
		BytesOut:   h.bytesOut,
		Broker:     h.cfg.Broker,
		SerialPort: h.cfg.Serial.Port,
	}
}

// Ser2MQStats 统计信息
type Ser2MQStats struct {
	Name       string `json:"name"`
	Running    bool   `json:"running"`
	BytesIn    uint64 `json:"bytes_in"`
	BytesOut   uint64 `json:"bytes_out"`
	Broker     string `json:"broker"`
	SerialPort string `json:"serial_port"`
}

// runSerialToMQTT 串口数据 -> MQTT
func (h *Ser2MQHandler) runSerialToMQTT() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("ser2mq %s runSerialToMQTT panic: %v", h.name, r)
		}
	}()

	buf := make([]byte, 4096)
	topic := h.mqtt.PublishTopic()

	for {
		select {
		case <-h.ctx.Done():
			return
		default:
		}

		n, err := h.serial.Read(buf)
		if err != nil {
			continue
		}
		if n == 0 {
			continue
		}

		data := buf[:n]

		// 加密并发布
		encrypted, err := h.crypto.Encrypt(data)
		if err != nil {
			log.Printf("ser2mq %s encrypt error: %v", h.name, err)
			continue
		}

		if err := h.mqtt.Publish(topic, encrypted); err != nil {
			log.Printf("ser2mq %s publish error: %v", h.name, err)
			continue
		}

		h.mu.Lock()
		h.bytesOut += uint64(n)
		h.mu.Unlock()
	}
}

// runMQTTToSerial MQTT -> 串口
func (h *Ser2MQHandler) runMQTTToSerial() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("ser2mq %s runMQTTToSerial panic: %v", h.name, r)
		}
	}()

	topic := h.mqtt.SubscribeTopic()

	// 订阅消息
	err := h.mqtt.Subscribe(topic, func(payload []byte) {
		h.handleMQTTMessage(payload)
	})
	if err != nil {
		log.Printf("ser2mq %s subscribe error: %v", h.name, err)
		return
	}

	// 保持运行直到 context 取消
	<-h.ctx.Done()
}

// handleMQTTMessage 处理收到的 MQTT 消息
func (h *Ser2MQHandler) handleMQTTMessage(payload []byte) {
	// 解密
	msg, err := DecryptPayload(payload, h.crypto)
	if err != nil {
		log.Printf("ser2mq %s decrypt error: %v", h.name, err)
		return
	}

	// 防重放检查（30秒窗口）
	now := time.Now().UnixMilli()
	if msg.Timestamp < h.lastTimestamp-30000 || msg.Timestamp > now+30000 {
		log.Printf("ser2mq %s replay detected, ts: %d", h.name, msg.Timestamp)
		return
	}
	h.lastTimestamp = msg.Timestamp

	// base64 解码串口数据
	data, err := base64.StdEncoding.DecodeString(msg.Data)
	if err != nil {
		log.Printf("ser2mq %s decode error: %v", h.name, err)
		return
	}

	// 写入串口
	if _, err := h.serial.Write(data); err != nil {
		log.Printf("ser2mq %s serial write error: %v", h.name, err)
		return
	}

	h.mu.Lock()
	h.bytesIn += uint64(len(data))
	h.mu.Unlock()
}
