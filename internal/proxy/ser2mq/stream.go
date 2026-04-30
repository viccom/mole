package ser2mq

import (
	"fmt"
	"sync"
	"time"
)

// PacketEvent ser2mq 报文事件（轻量摘要，不含完整原始数据）
type PacketEvent struct {
	Time       int64  `json:"time"`
	Tunnel     string `json:"tunnel"`
	Dir        string `json:"dir"`
	Length     int    `json:"length"`
	HexPreview string `json:"hex_preview"`
	Truncated  bool   `json:"truncated"`
	Message    string `json:"message,omitempty"`
}

// PacketSink 事件接收接口
type PacketSink interface {
	Publish(PacketEvent)
}

// StreamHub 按 tunnel 名路由的事件发布/订阅中心
type StreamHub struct {
	mu      sync.RWMutex
	tails   map[string][]PacketEvent
	subs    map[string]map[chan PacketEvent]struct{}
	tailCap int
}

// NewStreamHub 创建事件 hub，tailCap 为每隧道保留的最近事件数
func NewStreamHub(tailCap int) *StreamHub {
	if tailCap <= 0 {
		tailCap = 200
	}
	return &StreamHub{
		tails:   make(map[string][]PacketEvent),
		subs:    make(map[string]map[chan PacketEvent]struct{}),
		tailCap: tailCap,
	}
}

// Publish 发布事件到所有订阅者，并缓存到尾部队列
func (h *StreamHub) Publish(evt PacketEvent) {
	h.mu.Lock()

	// 缓存到 tail
	tail := h.tails[evt.Tunnel]
	tail = append(tail, evt)
	if len(tail) > h.tailCap {
		tail = tail[len(tail)-h.tailCap:]
	}
	h.tails[evt.Tunnel] = tail

	// 获取订阅者列表（复制引用）
	subs := h.subs[evt.Tunnel]
	snap := make([]chan PacketEvent, 0, len(subs))
	for ch := range subs {
		snap = append(snap, ch)
	}
	h.mu.Unlock()

	// 非阻塞发送
	for _, ch := range snap {
		select {
		case ch <- evt:
		default:
			// 订阅者太慢，丢弃事件
		}
	}
}

// Subscribe 订阅指定隧道的事件，返回事件 channel 和取消函数
// tail 为请求的历史事件数量，0 表示不需要历史
func (h *StreamHub) Subscribe(tunnel string, tail int) (<-chan PacketEvent, func()) {
	ch := make(chan PacketEvent, 64)

	h.mu.Lock()
	// 注册订阅者
	if h.subs[tunnel] == nil {
		h.subs[tunnel] = make(map[chan PacketEvent]struct{})
	}
	h.subs[tunnel][ch] = struct{}{}

	// 发送历史事件
	if tail > 0 {
		history := h.tails[tunnel]
		start := len(history) - tail
		if start < 0 {
			start = 0
		}
		for _, evt := range history[start:] {
			select {
			case ch <- evt:
			default:
			}
		}
	}
	h.mu.Unlock()

	unsub := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if subs, ok := h.subs[tunnel]; ok {
			delete(subs, ch)
			if len(subs) == 0 {
				delete(h.subs, tunnel)
			}
		}
	}

	return ch, unsub
}

// previewHex 生成数据的 hex 预览，maxBytes 为最大显示字节数
func previewHex(data []byte, maxBytes int) (string, bool) {
	if maxBytes <= 0 {
		maxBytes = 32
	}
	if len(data) <= maxBytes {
		return fmt.Sprintf("%x", data), false
	}
	return fmt.Sprintf("%x", data[:maxBytes]), true
}

// buildPacketEvent 构建报文事件
func buildPacketEvent(tunnel, dir string, data []byte) PacketEvent {
	hex, truncated := previewHex(data, 32)
	return PacketEvent{
		Time:       time.Now().UnixMilli(),
		Tunnel:     tunnel,
		Dir:        dir,
		Length:     len(data),
		HexPreview: hex,
		Truncated:  truncated,
	}
}

// buildStatusEvent 构建状态事件
func buildStatusEvent(tunnel, message string) PacketEvent {
	return PacketEvent{
		Time:    time.Now().UnixMilli(),
		Tunnel:  tunnel,
		Dir:     "status",
		Message: message,
	}
}
