package ser2net

import "sync"

// StreamHub 按 tunnel 名路由的 ser2net 事件中心。
type StreamHub struct {
	mu      sync.RWMutex
	tails   map[string][]PacketInfo
	subs    map[string]map[chan PacketInfo]struct{}
	tailCap int
}

func NewStreamHub(tailCap int) *StreamHub {
	if tailCap <= 0 {
		tailCap = 200
	}
	return &StreamHub{
		tails:   make(map[string][]PacketInfo),
		subs:    make(map[string]map[chan PacketInfo]struct{}),
		tailCap: tailCap,
	}
}

func (h *StreamHub) Publish(evt PacketInfo) {
	h.mu.Lock()
	tail := append(h.tails[evt.Tunnel], evt)
	if len(tail) > h.tailCap {
		tail = tail[len(tail)-h.tailCap:]
	}
	h.tails[evt.Tunnel] = tail

	subs := h.subs[evt.Tunnel]
	snap := make([]chan PacketInfo, 0, len(subs))
	for ch := range subs {
		snap = append(snap, ch)
	}
	h.mu.Unlock()

	for _, ch := range snap {
		select {
		case ch <- evt:
		default:
		}
	}
}

func (h *StreamHub) Subscribe(tunnel string, tail int) (<-chan PacketInfo, func()) {
	ch := make(chan PacketInfo, 64)

	h.mu.Lock()
	if h.subs[tunnel] == nil {
		h.subs[tunnel] = make(map[chan PacketInfo]struct{})
	}
	h.subs[tunnel][ch] = struct{}{}

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
