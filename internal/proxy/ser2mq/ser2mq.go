package ser2mq

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// Ser2MQConfig ser2mq 隧道配置
type Ser2MQConfig struct {
	Enable bool         `json:"enable"`
	Broker string       `json:"broker"`  // mqtt://user:pass@host:port
	Serial SerialConfig `json:"serial"`
	Secret string       `json:"secret"`   // 32字节 hex 字符串
	QoS    int          `json:"qos"`      // 0/1/2, 默认 1
}

// Ser2MQHandler ser2mq 隧道处理器
type Ser2MQHandler struct {
	name       string
	nodeID     string
	cfg        Ser2MQConfig
	crypto     *Crypto
	serial     SerialConn
	mqtt       *MQTTClient
	portName   string // SanitizePortName 缓存
	sink       PacketSink

	mu      sync.RWMutex
	running bool
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	bytesIn  atomic.Uint64
	bytesOut atomic.Uint64
}

// NewHandler 创建 ser2mq 处理器
func NewHandler(name, nodeID string, cfg Ser2MQConfig, sink PacketSink) (*Ser2MQHandler, error) {
	crypto, err := NewCrypto(cfg.Secret)
	if err != nil {
		return nil, fmt.Errorf("invalid crypto config: %w", err)
	}

	return &Ser2MQHandler{
		name:     name,
		nodeID:   nodeID,
		cfg:      cfg,
		crypto:   crypto,
		portName: SanitizePortName(cfg.Serial.Port),
		sink:     sink,
	}, nil
}

// Start 启动 ser2mq 隧道
func (h *Ser2MQHandler) Start(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.running {
		return fmt.Errorf("ser2mq tunnel %s already running", h.name)
	}

	serialConn, err := OpenSerial(h.cfg.Serial)
	if err != nil {
		return fmt.Errorf("open serial: %w", err)
	}
	h.serial = serialConn

	qos := byte(1)
	if h.cfg.QoS == 2 {
		qos = 2
	}

	mqttCfg := MQTTConfig{
		Broker: h.cfg.Broker,
		QoS:    qos,
	}
	mqttClient, err := NewMQTTClient(ctx, mqttCfg, h.nodeID, h.cfg.Serial.Port)
	if err != nil {
		h.serial.Close()
		h.serial = nil
		return fmt.Errorf("connect mqtt: %w", err)
	}
	h.mqtt = mqttClient

	h.ctx, h.cancel = context.WithCancel(ctx)
	h.running = true

	h.wg.Add(2)
	go h.runSerialToMQTT()
	go h.runMQTTToSerial()

	h.emitStatus("started")

	log.Printf("ser2mq tunnel %s started (broker: %s, serial: %s, pub: %s, sub: %s)",
		h.name, h.cfg.Broker, h.cfg.Serial.Port,
		h.mqtt.OutTopic(), h.mqtt.InTopic())
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
	cancel := h.cancel
	mqttCl := h.mqtt
	h.mqtt = nil
	h.mu.Unlock()

	if mqttCl != nil {
		mqttCl.Disconnect()
	}
	if cancel != nil {
		cancel()
	}

	h.wg.Wait()

	h.mu.Lock()
	if h.serial != nil {
		h.serial.Close()
		h.serial = nil
	}
	h.crypto = nil
	h.mu.Unlock()

	log.Printf("ser2mq tunnel %s stopped", h.name)
}

// emitPacket 发送报文事件（nil-safe）
func (h *Ser2MQHandler) emitPacket(dir string, data []byte) {
	if h.sink == nil {
		return
	}
	h.sink.Publish(buildPacketEvent(h.name, dir, data))
}

// emitStatus 发送状态事件（nil-safe）
func (h *Ser2MQHandler) emitStatus(message string) {
	if h.sink == nil {
		return
	}
	h.sink.Publish(buildStatusEvent(h.name, message))
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
		Name:          h.name,
		Running:       h.running,
		BytesIn:       h.bytesIn.Load(),
		BytesOut:      h.bytesOut.Load(),
		Broker:        h.cfg.Broker,
		SerialPort:    h.cfg.Serial.Port,
		MQTTConnected: h.mqtt != nil && h.mqtt.IsConnected(),
		SerialOpen:    h.serial != nil,
	}
}

// Ser2MQStats 统计信息
type Ser2MQStats struct {
	Name          string `json:"name"`
	Running       bool   `json:"running"`
	BytesIn       uint64 `json:"bytes_in"`
	BytesOut      uint64 `json:"bytes_out"`
	Broker        string `json:"broker"`
	SerialPort    string `json:"serial_port"`
	MQTTConnected bool   `json:"mqtt_connected"`
	SerialOpen    bool   `json:"serial_open"`
	Error         string `json:"error,omitempty"`
	ErrorPhase    string `json:"error_phase,omitempty"`
}

// runSerialToMQTT 串口数据 → MQTT
// 作为 Acceptor：串口数据是 "output"，发布到 /out topic
func (h *Ser2MQHandler) runSerialToMQTT() {
	defer h.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			log.Printf("ser2mq %s runSerialToMQTT panic: %v", h.name, r)
		}
	}()

	// goroutine 启动时缓存不变引用，避免每条消息加锁
	h.mu.RLock()
	crypto := h.crypto
	mqttCl := h.mqtt
	serial := h.serial
	topic := mqttCl.OutTopic()
	h.mu.RUnlock()

	buf := make([]byte, 4096)

	for {
		select {
		case <-h.ctx.Done():
			return
		default:
		}

		n, err := serial.Read(buf)
		if err != nil || n == 0 {
			if err != nil && !isTimeout(err) {
				log.Printf("ser2mq %s serial read error: %v", h.name, err)
			}
			continue
		}

		data := buf[:n]
		h.emitPacket("serial_out", data)

		msgBytes, err := BuildMQTTMessage(h.nodeID, h.portName, data)
		if err != nil {
			log.Printf("ser2mq %s build message error: %v", h.name, err)
			continue
		}

		encrypted, encErr := crypto.Encrypt(msgBytes)
		if encErr != nil {
			log.Printf("ser2mq %s encrypt error: %v", h.name, encErr)
			continue
		}

		if pubErr := mqttCl.Publish(topic, encrypted); pubErr != nil {
			log.Printf("ser2mq %s publish error: %v", h.name, pubErr)
			continue
		}

		h.emitPacket("mqtt_pub", data)
		h.bytesOut.Add(uint64(n))
	}
}

// runMQTTToSerial MQTT → 串口
// 作为 Acceptor：订阅 /in topic，收到的数据写入串口
func (h *Ser2MQHandler) runMQTTToSerial() {
	defer h.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			log.Printf("ser2mq %s runMQTTToSerial panic: %v", h.name, r)
		}
	}()

	h.mu.RLock()
	mqttCl := h.mqtt
	crypto := h.crypto
	serial := h.serial
	topic := mqttCl.InTopic()
	h.mu.RUnlock()

	err := mqttCl.Subscribe(topic, func(payload []byte) {
		h.handleMQTTMessage(crypto, serial, payload)
	})
	if err != nil {
		log.Printf("ser2mq %s subscribe error: %v", h.name, err)
		return
	}

	<-h.ctx.Done()
}

// handleMQTTMessage 处理收到的 MQTT 消息
// 兼容两种格式：MQTTMessage JSON 包装 + 原始字节（向后兼容）
func (h *Ser2MQHandler) handleMQTTMessage(crypto *Crypto, serial SerialConn, payload []byte) {
	plaintext, err := crypto.Decrypt(payload)
	if err != nil {
		log.Printf("ser2mq %s decrypt error: %v", h.name, err)
		return
	}

	var data []byte

	var msg MQTTMessage
	if err := json.Unmarshal(plaintext, &msg); err == nil && msg.Data != "" {
		decoded, err := base64.StdEncoding.DecodeString(msg.Data)
		if err != nil {
			log.Printf("ser2mq %s base64 decode error: %v", h.name, err)
			return
		}
		data = decoded

		if msg.Timestamp > 0 {
			now := time.Now().UnixMilli()
			if msg.Timestamp < now-30000 || msg.Timestamp > now+30000 {
				log.Printf("ser2mq %s replay detected, ts: %d", h.name, msg.Timestamp)
				return
			}
		}
	} else {
		data = plaintext
	}
	h.emitPacket("mqtt_sub", data)

	if _, err := serial.Write(data); err != nil {
		log.Printf("ser2mq %s serial write error: %v", h.name, err)
		return
	}

	h.bytesIn.Add(uint64(len(data)))
	h.emitPacket("serial_in", data)
}
