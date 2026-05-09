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
	tcpClients *connSet
	tcpMu      sync.Mutex

	bytesIn  atomic.Uint64
	bytesOut atomic.Uint64
	clients  atomic.Int32
	running  atomic.Bool
	lastErr  atomic.Value // string

	recentErr       atomic.Value // string
	recentErrAtMs   atomic.Int64
	lastRxUnixMs    atomic.Int64
	lastTxUnixMs    atomic.Int64
	onPacket        func(PacketInfo)
	cleanupOnce     sync.Once
}

func NewHandler(name, typ string, cfg Ser2NetConfig) (*Handler, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	h := &Handler{name: name, typ: typ, cfg: cfg}
	h.lastErr.Store("")
	h.recentErr.Store("")
	return h, nil
}

func (h *Handler) Start(ctx context.Context) error {
	if h.running.Load() {
		return nil
	}
	h.cleanupOnce = sync.Once{}
	h.ctx, h.cancel = context.WithCancel(ctx)

	serial, err := OpenSerial(h.cfg.Serial)
	if err != nil {
		return err
	}
	h.serial = serial
	h.clearError()
	h.clearRecentError()
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
	h.cleanup()
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
	ServerListening  bool   `json:"server_listening,omitempty"`
	ClientConnected  bool   `json:"client_connected,omitempty"`
	LastRxUnixMs     int64  `json:"last_rx_unix_ms,omitempty"`
	LastTxUnixMs     int64  `json:"last_tx_unix_ms,omitempty"`
	RecentError      string `json:"recent_error,omitempty"`
	RecentErrorAtUnixMs int64 `json:"recent_error_at_unix_ms,omitempty"`
	Error      string `json:"error,omitempty"`
}

func (h *Handler) Stats() Stats {
	errText := ""
	if v := h.lastErr.Load(); v != nil {
		if s, ok := v.(string); ok {
			errText = s
		}
	}
	recent := ""
	if v := h.recentErr.Load(); v != nil {
		if s, ok := v.(string); ok {
			recent = s
		}
	}
	clients := int(h.clients.Load())
	running := h.running.Load()
	return Stats{
		Name:       h.name,
		Type:       h.typ,
		Mode:       h.cfg.Mode,
		Running:    running,
		SerialOpen: h.serial != nil,
		SerialPort: h.cfg.Serial.Port,
		Address:    h.cfg.Address,
		Clients:    clients,
		BytesIn:    h.bytesIn.Load(),
		BytesOut:   h.bytesOut.Load(),
		ServerListening: h.cfg.Mode == "server" && running && h.getListener() != nil,
		ClientConnected: h.cfg.Mode == "client" && clients > 0,
		LastRxUnixMs: h.lastRxUnixMs.Load(),
		LastTxUnixMs: h.lastTxUnixMs.Load(),
		RecentError: recent,
		RecentErrorAtUnixMs: h.recentErrAtMs.Load(),
		Error:      errText,
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
		h.setRecentError(err)
		return n, err
	}
	h.bytesIn.Add(uint64(n))
	h.lastRxUnixMs.Store(time.Now().UnixMilli())
	h.emitPacket("SERIAL_IN", data[:n])
	return n, nil
}

func (h *Handler) cleanup() {
	h.cleanupOnce.Do(func() {
		h.running.Store(false)
		if h.cancel != nil {
			h.cancel()
		}
		h.closeTCPClients()
		h.closeListener()
		h.closeSerial()
	})
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

func (h *Handler) getListener() io.Closer {
	h.listenerMu.Lock()
	defer h.listenerMu.Unlock()
	return h.listener
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

func (h *Handler) setTCPClients(cs *connSet) {
	h.tcpMu.Lock()
	h.tcpClients = cs
	h.tcpMu.Unlock()
}

func (h *Handler) closeTCPClients() {
	h.tcpMu.Lock()
	cs := h.tcpClients
	h.tcpClients = nil
	h.tcpMu.Unlock()
	if cs != nil {
		cs.closeAll()
		h.clients.Store(0)
	}
}

func (h *Handler) setError(err error) {
	if err == nil {
		return
	}
	h.lastErr.Store(err.Error())
	h.running.Store(false)
}

func (h *Handler) setTransientError(err error) {
	if err == nil {
		return
	}
	h.lastErr.Store(err.Error())
}

func (h *Handler) clearError() {
	h.lastErr.Store("")
}

func (h *Handler) setRecentError(err error) {
	if err == nil {
		return
	}
	h.recentErr.Store(err.Error())
	h.recentErrAtMs.Store(time.Now().UnixMilli())
}

func (h *Handler) clearRecentError() {
	h.recentErr.Store("")
	h.recentErrAtMs.Store(0)
}

func (h *Handler) SetPacketHook(onPacket func(PacketInfo)) {
	h.onPacket = onPacket
}

func (h *Handler) emitPacket(dir string, data []byte) {
	if h.onPacket == nil {
		return
	}
	hexStr := fmt.Sprintf("%x", data)
	if len(hexStr) > 200 {
		hexStr = hexStr[:200] + "..."
	}
	h.onPacket(PacketInfo{
		Time:    time.Now().Format("15:04:05.000"),
		Dir:     dir,
		Tunnel:  h.name,
		DataHex: hexStr,
		DataLen: len(data),
	})
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
	conns := make([]net.Conn, 0, len(cs.conns))
	for conn := range cs.conns {
		conns = append(conns, conn)
	}
	cs.mu.RUnlock()

	for _, conn := range conns {
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

func (cs *connSet) closeAll() {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	for conn := range cs.conns {
		_ = conn.Close()
	}
	cs.conns = make(map[net.Conn]struct{})
}
