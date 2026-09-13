//go:build p2p

package tunnel

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
)

// StreamOpener 开一条新的多路复用 stream（initiator 侧 bridgeTCP / StartUDPListener 用）。
// session.StreamMux 满足该接口；tunnel 包不 import session 以避免循环依赖。
type StreamOpener interface {
	OpenStream() (net.Conn, error)
}

// ---- Types ----

// Params defines a tunnel's configuration.
type Params struct {
	Protocol   string // "tcp" or "udp"
	LocalPort  int    // port that B listens on
	TargetHost string // host that A forwards to
	TargetPort int    // port that A forwards to
}

// Stats holds accumulated tunnel statistics.
//
// BytesIn/BytesOut 用 atomic.Uint64 而非裸 uint64：32 位平台（arm/386）的 64 位原子操作
// 要求 8 字节对齐，而 Stats 作为 Tunnel 的非首字段（前面有 ID/Params），裸 uint64 在 32 位
// 只 4 字节对齐，触发 "unaligned 64-bit atomic operation" panic（OpenWrt armv7 实测崩于
// ListTunnels/CreateTunnel 读 Stats）。atomic.Uint64 类型在所有平台自带 8 字节对齐保证。
type Stats struct {
	BytesIn  atomic.Uint64
	BytesOut atomic.Uint64
}

// Status is a tunnel's lifecycle state.
type Status int

const (
	StatusPending Status = iota
	StatusActive
	StatusClosing
	StatusClosed
)

// Tunnel represents an active port mapping.
type Tunnel struct {
	ID     uint32
	Params Params
	Stats  Stats       // use AddIn/AddOut for atomic updates

	status      Status
	mu          sync.Mutex
	closer      io.Closer   // local listener; closed on stop
	done        chan struct{}
	activeConns []io.Closer // 活跃数据 conn（st/local/remote），stop 关闭以中断转发（C3）
}

func (t *Tunnel) addIn(n uint64)  { t.Stats.BytesIn.Add(n) }
func (t *Tunnel) addOut(n uint64) { t.Stats.BytesOut.Add(n) }

func (t *Tunnel) setStatus(s Status) {
	t.mu.Lock()
	t.status = s
	t.mu.Unlock()
}

func (t *Tunnel) Status() Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status
}

// StringID returns the human-readable tunnel identifier.
func (t *Tunnel) StringID() string { return fmt.Sprintf("tun-%d", t.ID) }

// trackConn 注册活跃数据 conn，使 stop 能关闭（C3：CloseTunnel 中断活跃转发，
// 不留僵尸连接）。若注册时 tunnel 已 stop，立即关闭该 conn 防泄漏。
func (t *Tunnel) trackConn(c io.Closer) {
	t.mu.Lock()
	select {
	case <-t.done:
		t.mu.Unlock()
		c.Close()
		return
	default:
		t.activeConns = append(t.activeConns, c)
		t.mu.Unlock()
	}
}

// untrackConn 在数据 conn 自然关闭后移除注册（避免 stop 重复关闭已关 conn）。
func (t *Tunnel) untrackConn(c io.Closer) {
	t.mu.Lock()
	for i, x := range t.activeConns {
		if x == c {
			t.activeConns = append(t.activeConns[:i], t.activeConns[i+1:]...)
			break
		}
	}
	t.mu.Unlock()
}

func (t *Tunnel) stop() {
	t.mu.Lock()
	if t.closer != nil {
		t.closer.Close()
	}
	conns := t.activeConns
	t.activeConns = nil
	select {
	case <-t.done:
	default:
		close(t.done)
	}
	t.mu.Unlock()
	// 在锁外关闭数据 conn（避免长 Close 持锁，也避免与 trackConn 的锁竞争）
	for _, c := range conns {
		c.Close()
	}
}

// ---- Manager ----

// Manager owns all tunnels over a single P2P connection.
type Manager struct {
	mux     StreamOpener
	mu      sync.Mutex
	tunnels map[uint32]*Tunnel
	nextID  uint32

	sendLine func(string) // thread-safe write to control stream
}

// NewManager creates a tunnel manager.
//
//	mux provides OpenStream for tunnel data streams; sendLine must be a
//	thread-safe function that writes a line to the control stream.
func NewManager(mux StreamOpener, sendLine func(string)) *Manager {
	return &Manager{
		mux:      mux,
		tunnels:  make(map[uint32]*Tunnel),
		sendLine: sendLine,
	}
}

// allocID 分配一个随机 tunnel ID。用 crypto/rand 而非递增计数——两端都从 1 开始递增
// 时，对端 OPEN 的 tunnelID 会与本端 initiator 碰撞，acceptor AcceptRemote 不检查
// 冲突直接 m.tunnels[uid]=t 覆盖本端 initiator，导致该 tunnel 永远等不到自己的 OK
// （实测 :6670 待连接根因）。随机 uint32 碰撞概率 ~1/2^32，多隧道仍极低；仍跳已占用
// + 避 0 + OPEN 冲突检测（下方 AcceptRemote）三重保险。
func (m *Manager) allocID() uint32 {
	for {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			id := atomic.AddUint32(&m.nextID, 1)
			log.Printf("[tunnel] allocID=%d (⚠️ CRYPTO RAND FAILED, fallback 递增——两端可能碰撞)", id)
			return id
		}
		id := binary.BigEndian.Uint32(b[:])
		if id == 0 {
			continue
		}
		m.mu.Lock()
		occupied := m.tunnels[id] != nil
		m.mu.Unlock()
		if !occupied {
			log.Printf("[tunnel] allocID=%d (crypto/rand 随机)", id)
			return id
		}
		log.Printf("[tunnel] allocID=%d occupied, retry", id)
	}
}

func (m *Manager) send(msg string) {
	m.sendLine(msg)
}

// ---- Public API ----

// Create initiates a tunnel (initiator side). Sends TUNNEL:OPEN to the peer.
// The caller must listen for TUNNEL:OK via HandleControl, then start the
// local listener and call StartAcceptLoop.
func (m *Manager) Create(params Params) (*Tunnel, error) {
	if params.Protocol != "tcp" && params.Protocol != "udp" {
		return nil, fmt.Errorf("unknown protocol: %s", params.Protocol)
	}

	t := &Tunnel{
		ID:     m.allocID(),
		Params: params,
		done:   make(chan struct{}),
		status: StatusPending,
	}

	m.mu.Lock()
	m.tunnels[t.ID] = t
	m.mu.Unlock()

	m.send(fmt.Sprintf("TUNNEL:OPEN:%d:%s:%d:%s:%d",
		t.ID, params.Protocol, params.LocalPort, params.TargetHost, params.TargetPort))
	log.Printf("[tunnel] %s created (initiator :%d → %s:%d), sent OPEN uid=%d, waiting peer OK",
		t.StringID(), params.LocalPort, params.TargetHost, params.TargetPort, t.ID)

	return t, nil
}

// AcceptRemote handles an incoming TUNNEL:OPEN from the peer.
// Called by HandleControl when the peer initiates a tunnel.
func (m *Manager) AcceptRemote(fields []string) error {
	if len(fields) != 5 {
		return fmt.Errorf("invalid TUNNEL:OPEN: expected 5 fields, got %d", len(fields))
	}
	var uid uint32
	if _, err := fmt.Sscanf(fields[0], "%d", &uid); err != nil {
		return fmt.Errorf("invalid uid %q: %w", fields[0], err)
	}
	proto := fields[1]
	if proto != "tcp" && proto != "udp" {
		m.send(fmt.Sprintf("TUNNEL:ERR:%d:bad-protocol", uid))
		return fmt.Errorf("invalid protocol %q", proto)
	}
	var lport, rport int
	if _, err := fmt.Sscanf(fields[2], "%d", &lport); err != nil {
		return fmt.Errorf("invalid localPort %q: %w", fields[2], err)
	}
	// fields: [3]=host, [4]=port
	host := fields[3]
	if _, err := fmt.Sscanf(fields[4], "%d", &rport); err != nil {
		return fmt.Errorf("invalid remotePort %q: %w", fields[4], err)
	}

	params := Params{
		Protocol:   proto,
		LocalPort:  lport,
		TargetHost: host,
		TargetPort: rport,
	}

	t := &Tunnel{
		ID:     uid,
		Params: params,
		done:   make(chan struct{}),
		status: StatusActive,
	}

	m.mu.Lock()
	if _, exists := m.tunnels[uid]; exists {
		// 冲突：uid 已被本地 initiator 占用（两端随机碰撞，极罕见）。
		// 不覆盖——回 ERR 让发起方重试换 ID，避免 initiator 状态丢失。
		m.mu.Unlock()
		log.Printf("[tunnel] OPEN uid=%d conflicts with local tunnel, reject", uid)
		m.send(fmt.Sprintf("TUNNEL:ERR:%d:conflict", uid))
		return fmt.Errorf("tunnel %d conflicts with local tunnel", uid)
	}
	m.tunnels[t.ID] = t
	m.mu.Unlock()

	m.send(fmt.Sprintf("TUNNEL:OK:%d", t.ID))
	log.Printf("[tunnel] accepted %s (%s -> %s:%d)", t.StringID(), proto, host, rport)
	return nil
}

// HandleControl processes an incoming tunnel control message.
// Returns true if the line was consumed.
// Must be called for every line read from the main heartbeat stream.
func (m *Manager) HandleControl(line string) bool {
	switch {
	case strings.HasPrefix(line, ctrlOK):
		var uid uint32
		fmt.Sscanf(strings.TrimPrefix(line, ctrlOK), "%d", &uid)
		m.mu.Lock()
		t, ok := m.tunnels[uid]
		m.mu.Unlock()
		if ok {
			t.setStatus(StatusActive)
			log.Printf("[tunnel] %s activated", t.StringID())
		}
		return true

	case strings.HasPrefix(line, ctrlErr):
		rest := strings.TrimPrefix(line, ctrlErr)
		parts := strings.SplitN(rest, ":", 2)
		var uid uint32
		fmt.Sscanf(parts[0], "%d", &uid)
		reason := ""
		if len(parts) > 1 {
			reason = parts[1]
		}
		m.mu.Lock()
		t, ok := m.tunnels[uid]
		if ok {
			delete(m.tunnels, uid)
		}
		m.mu.Unlock()
		if ok {
			t.setStatus(StatusClosed)
			t.stop()
			log.Printf("[tunnel] %s rejected: %s", t.StringID(), reason)
		}
		return true

	case strings.HasPrefix(line, ctrlClose):
		var uid uint32
		fmt.Sscanf(strings.TrimPrefix(line, ctrlClose), "%d", &uid)
		m.mu.Lock()
		t, ok := m.tunnels[uid]
		if ok {
			delete(m.tunnels, uid)
		}
		m.mu.Unlock()
		if ok {
			t.setStatus(StatusClosed)
			t.stop()
			m.send(fmt.Sprintf("TUNNEL:CLOSED:%d", uid))
			log.Printf("[tunnel] %s closed by peer", t.StringID())
		}
		return true

	case strings.HasPrefix(line, ctrlCloseAck):
		var uid uint32
		fmt.Sscanf(strings.TrimPrefix(line, ctrlCloseAck), "%d", &uid)
		m.mu.Lock()
		t, ok := m.tunnels[uid]
		if ok {
			delete(m.tunnels, uid)
		}
		m.mu.Unlock()
		if ok {
			t.setStatus(StatusClosed)
			t.stop()
			log.Printf("[tunnel] %s closed", t.StringID())
		}
		return true

	case strings.HasPrefix(line, "TUNNEL:OPEN:"):
		fields, err := ParseTunnelOpen(strings.TrimPrefix(line, "TUNNEL:OPEN:"))
		if err != nil {
			log.Printf("[tunnel] bad OPEN: %v", err)
			return true
		}
		if err := m.AcceptRemote(fields); err != nil {
			log.Printf("[tunnel] bad OPEN: %v", err)
		}
		return true
	}
	return false
}

// ParseTunnelOpen parses the payload of a TUNNEL:OPEN control line into
// exactly 5 fields: [uid, proto, localPort, targetHost, targetPort].
//
// Format: "<uid>:<proto>:<localPort>:<targetHost>:<targetPort>"
//
// 前 3 个字段 uid:proto:localPort 不含冒号，strings.SplitN(":", 4) 把它们切出，
// 第 4 段是 "targetHost:targetPort"，再用 net.SplitHostPort 解析。SplitHostPort
// 能正确处理 IPv6 字面量——约定 IPv6 target 用 bracket 形式（[fe80::1]:443），
// 这与 tcp.go/udp.go 在 dial 处用 net.JoinHostPort 输出的形式一致；返回的 host
// 已去 bracket（裸 IPv6），JoinHostPort 会重新加上。
func ParseTunnelOpen(rest string) ([]string, error) {
	left := strings.SplitN(rest, ":", 4)
	if len(left) != 4 {
		return nil, fmt.Errorf("expected uid:proto:localPort:host:port, got %d fields in %q", len(left), rest)
	}
	host, port, err := net.SplitHostPort(left[3])
	if err != nil {
		return nil, fmt.Errorf("parse target host:port %q: %w", left[3], err)
	}
	return []string{left[0], left[1], left[2], host, port}, nil
}

// HandleDataStream dispatches an incoming data stream by its magic number.
// magic is the 4-byte header already read from st.
func (m *Manager) HandleDataStream(magic uint32, st net.Conn) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(st, header); err != nil {
		log.Printf("[tunnel] data stream header: %v", err)
		st.Close()
		return
	}
	uid := u32(header)

	m.mu.Lock()
	t, ok := m.tunnels[uid]
	m.mu.Unlock()
	if !ok {
		log.Printf("[tunnel] unknown tunnel %d in data stream", uid)
		st.Close()
		return
	}

	switch magic {
	case TCPMagic:
		go handleTCPStream(t, st)
	case UDPMagic:
		go handleUDPStream(t, st, false)
	default:
		st.Close()
	}
}

// Close closes a tunnel by ID.
func (m *Manager) Close(uid uint32) error {
	m.mu.Lock()
	t, ok := m.tunnels[uid]
	if ok {
		delete(m.tunnels, uid)
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("tunnel %d not found", uid)
	}
	t.setStatus(StatusClosing)
	m.send(fmt.Sprintf("TUNNEL:CLOSE:%d", uid))
	t.stop()
	return nil
}

// List returns all active tunnels.
func (m *Manager) List() []*Tunnel {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Tunnel, 0, len(m.tunnels))
	for _, t := range m.tunnels {
		out = append(out, t)
	}
	return out
}

// Shutdown closes all tunnels.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	ids := make([]uint32, 0, len(m.tunnels))
	for id := range m.tunnels {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Close(id)
	}
}
