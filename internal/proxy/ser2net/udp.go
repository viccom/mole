package ser2net

import (
	"log"
	"net"
	"sync"
	"time"
)

const udpPeerTimeout = 5 * time.Minute

type peerEntry struct {
	addr     *net.UDPAddr
	lastSeen time.Time
}

func (h *Handler) runUDPServer() {
	defer h.cleanup()

	addr, err := net.ResolveUDPAddr("udp", h.cfg.Address)
	if err != nil {
		log.Printf("ser2net: %s udp server resolve error: %v", h.name, err)
		return
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		log.Printf("ser2net: %s udp server listen error: %v", h.name, err)
		return
	}
	defer conn.Close()
	log.Printf("ser2net: %s udp server listening on %s", h.name, h.cfg.Address)

	peers := &peerSet{m: make(map[string]*peerEntry)}

	go h.serialToUDPPeers(conn, peers)
	h.udpToSerial(conn, peers)
}

func (h *Handler) runUDPClient() {
	defer h.cleanup()

	raddr, err := net.ResolveUDPAddr("udp", h.cfg.Address)
	if err != nil {
		log.Printf("ser2net: %s udp client resolve error: %v", h.name, err)
		return
	}

	for {
		select {
		case <-h.ctx.Done():
			return
		default:
		}

		conn, err := net.DialUDP("udp", nil, raddr)
		if err != nil {
			log.Printf("ser2net: %s udp client dial error: %v", h.name, err)
			select {
			case <-h.ctx.Done():
				return
			case <-time.After(3 * time.Second):
				continue
			}
		}

		log.Printf("ser2net: %s udp client connected to %s", h.name, h.cfg.Address)
		h.relayUDPClient(conn)
		conn.Close()

		select {
		case <-h.ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

func (h *Handler) relayUDPClient(conn *net.UDPConn) {
	done := make(chan struct{})
	var once sync.Once
	closeDone := func() { once.Do(func() { close(done) }) }

	// UDP → Serial
	go func() {
		defer closeDone()
		buf := make([]byte, 4096)
		for {
			select {
			case <-done:
				return
			default:
			}
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			n, err := conn.Read(buf)
			if err != nil {
				if isNetTimeout(err) {
					continue
				}
				return
			}
			if n > 0 {
				h.writeSerial(buf[:n])
			}
		}
	}()

	// Serial → UDP
	func() {
		defer closeDone()
		buf := make([]byte, 4096)
		for {
			select {
			case <-done:
				return
			default:
			}
			n, err := h.serial.Read(buf)
			if err != nil {
				if isTimeout(err) {
					continue
				}
				return
			}
			if n > 0 {
				h.bytesOut.Add(uint64(n))
				conn.Write(buf[:n])
			}
		}
	}()
}

func (h *Handler) serialToUDPPeers(conn *net.UDPConn, peers *peerSet) {
	buf := make([]byte, 4096)
	for {
		select {
		case <-h.ctx.Done():
			return
		default:
		}
		n, err := h.serial.Read(buf)
		if err != nil {
			if isTimeout(err) {
				continue
			}
			return
		}
		if n > 0 {
			h.bytesOut.Add(uint64(n))
			data := make([]byte, n)
			copy(data, buf[:n])
			for _, addr := range peers.active() {
				conn.WriteToUDP(data, addr)
			}
		}
	}
}

func (h *Handler) udpToSerial(conn *net.UDPConn, peers *peerSet) {
	buf := make([]byte, 4096)
	for {
		select {
		case <-h.ctx.Done():
			return
		default:
		}
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, remoteAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if isNetTimeout(err) {
				continue
			}
			log.Printf("ser2net: %s udp read error: %v", h.name, err)
			return
		}
		if n > 0 {
			peers.track(remoteAddr)
			h.clients.Store(int32(peers.count()))
			h.writeSerial(buf[:n])
		}
	}
}

type peerSet struct {
	mu sync.RWMutex
	m  map[string]*peerEntry
}

func (ps *peerSet) track(addr *net.UDPAddr) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.m[addr.String()] = &peerEntry{addr: addr, lastSeen: time.Now()}
}

func (ps *peerSet) active() []*net.UDPAddr {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	now := time.Now()
	result := make([]*net.UDPAddr, 0, len(ps.m))
	for key, entry := range ps.m {
		if now.Sub(entry.lastSeen) > udpPeerTimeout {
			delete(ps.m, key)
			continue
		}
		result = append(result, entry.addr)
	}
	return result
}

func (ps *peerSet) count() int {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return len(ps.m)
}
