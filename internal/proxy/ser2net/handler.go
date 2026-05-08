package ser2net

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type Handler struct {
	name   string
	typ    string
	cfg    Ser2NetConfig
	ctx    context.Context
	cancel context.CancelFunc

	serial  SerialConn
	writeMu sync.Mutex

	listener   io.Closer
	listenerMu sync.Mutex

	bytesIn  atomic.Uint64
	bytesOut atomic.Uint64
	clients  atomic.Int32
	running  atomic.Bool
}

func NewHandler(name, typ string, cfg Ser2NetConfig) (*Handler, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Handler{name: name, typ: typ, cfg: cfg}, nil
}

func (h *Handler) Start(ctx context.Context) error {
	h.ctx, h.cancel = context.WithCancel(ctx)

	serial, err := OpenSerial(h.cfg.Serial)
	if err != nil {
		return err
	}
	h.serial = serial
	h.running.Store(true)

	switch {
	case h.typ == "ser2tcp" && h.cfg.Mode == "server":
		go h.runTCPServer()
	case h.typ == "ser2tcp" && h.cfg.Mode == "client":
		go h.runTCPClient()
	case h.typ == "ser2udp" && h.cfg.Mode == "server":
		go h.runUDPServer()
	case h.typ == "ser2udp" && h.cfg.Mode == "client":
		go h.runUDPClient()
	default:
		h.serial.Close()
		h.running.Store(false)
		return fmt.Errorf("unknown type/mode: %s/%s", h.typ, h.cfg.Mode)
	}
	return nil
}

func (h *Handler) Stop() {
	h.running.Store(false)
	if h.cancel != nil {
		h.cancel()
	}
	h.closeListener()
	h.closeSerial()
}

func (h *Handler) IsRunning() bool {
	return h.running.Load()
}

type Stats struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Mode       string `json:"mode"`
	Running    bool   `json:"running"`
	SerialOpen bool   `json:"serial_open"`
	SerialPort string `json:"serial_port"`
	Address    string `json:"address"`
	Clients    int    `json:"clients"`
	BytesIn    uint64 `json:"bytes_in"`
	BytesOut   uint64 `json:"bytes_out"`
	Error      string `json:"error,omitempty"`
}

func (h *Handler) Stats() Stats {
	return Stats{
		Name:       h.name,
		Type:       h.typ,
		Mode:       h.cfg.Mode,
		Running:    h.running.Load(),
		SerialOpen: h.serial != nil,
		SerialPort: h.cfg.Serial.Port,
		Address:    h.cfg.Address,
		Clients:    int(h.clients.Load()),
		BytesIn:    h.bytesIn.Load(),
		BytesOut:   h.bytesOut.Load(),
	}
}

func (h *Handler) writeSerial(data []byte) (int, error) {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	if h.serial == nil {
		return 0, fmt.Errorf("serial port closed")
	}
	n, err := h.serial.Write(data)
	if err != nil {
		return n, err
	}
	h.bytesIn.Add(uint64(n))
	return n, nil
}

func (h *Handler) cleanup() {
	h.running.Store(false)
	h.closeListener()
	h.closeSerial()
}

func (h *Handler) maxConn() int {
	if h.cfg.MaxConn <= 0 {
		return 1
	}
	return h.cfg.MaxConn
}

func (h *Handler) setListener(closer io.Closer) {
	h.listenerMu.Lock()
	h.listener = closer
	h.listenerMu.Unlock()
}

func (h *Handler) closeListener() {
	h.listenerMu.Lock()
	listener := h.listener
	h.listener = nil
	h.listenerMu.Unlock()
	if listener != nil {
		_ = listener.Close()
	}
}

func (h *Handler) closeSerial() {
	h.writeMu.Lock()
	serial := h.serial
	h.serial = nil
	h.writeMu.Unlock()
	if serial != nil {
		_ = serial.Close()
	}
}

// connSet manages TCP server client connections
type connSet struct {
	mu    sync.RWMutex
	conns map[net.Conn]struct{}
	max   int
}

func newConnSet(max int) *connSet {
	return &connSet{conns: make(map[net.Conn]struct{}), max: max}
}

func (cs *connSet) add(conn net.Conn) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.max > 0 && len(cs.conns) >= cs.max {
		for old := range cs.conns {
			old.Close()
			delete(cs.conns, old)
			break
		}
	}
	cs.conns[conn] = struct{}{}
	return true
}

func (cs *connSet) remove(conn net.Conn) {
	cs.mu.Lock()
	delete(cs.conns, conn)
	cs.mu.Unlock()
}

func (cs *connSet) broadcast(data []byte) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	for conn := range cs.conns {
		conn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
		if _, err := conn.Write(data); err != nil {
			log.Printf("ser2net: write to client %s failed: %v", conn.RemoteAddr(), err)
		}
	}
}

func (cs *connSet) len() int {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return len(cs.conns)
}
