package ser2net

import (
	"bytes"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

const udpPeerTimeout = 5 * time.Minute

var udpProbePayload = []byte("__MOLE_UDP_PROBE__")

type peerEntry struct {
	addr     *net.UDPAddr
	lastSeen time.Time
}

func (h *Handler) runUDPServer() {
	defer h.cleanup()

	addr, err := net.ResolveUDPAddr("udp", h.cfg.Address)
	if err != nil {
		log.Printf("ser2net: %s udp server resolve error: %v", h.name, err)
		h.setError(err)
		return
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		log.Printf("ser2net: %s udp server listen error: %v", h.name, err)
		h.setError(err)
		return
	}
	h.setListener(conn)
	defer conn.Close()
	defer h.setListener(nil)
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
		h.setError(err)
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
			h.setTransientError(err)
			select {
			case <-h.ctx.Done():
				return
			case <-time.After(3 * time.Second):
				continue
			}
		}

		log.Printf("ser2net: %s udp client connected to %s", h.name, h.cfg.Address)
		h.clearError()
		h.running.Store(true)
		h.clients.Store(1)
		if _, err := conn.Write(udpProbePayload); err != nil {
			log.Printf("ser2net: %s udp client probe failed: %v", h.name, err)
			h.setRecentError(err)
		}
		h.relayUDPClient(conn)
		h.clients.Store(0)
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
				log.Printf("ser2net: %s udp client read error: %v", h.name, err)
				return
			}
			if n > 0 {
				h.emitPacket("UDP_IN", buf[:n])
				if _, err := h.writeSerial(buf[:n]); err != nil {
					log.Printf("ser2net: %s write serial from udp client failed: %v", h.name, err)
					return
				}
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
			h.writeMu.Lock()
			serial := h.serial
			h.writeMu.Unlock()
			if serial == nil {
				return
			}
			n, err := serial.Read(buf)
			if err != nil {
				if err == io.EOF {
					time.Sleep(20 * time.Millisecond)
					continue
				}
				if isTimeout(err) {
					continue
				}
				log.Printf("ser2net: %s read serial error in udp client: %v", h.name, err)
				h.setRecentError(err)
				time.Sleep(100 * time.Millisecond)
				continue
			}
			if n > 0 {
				h.bytesOut.Add(uint64(n))
				h.lastTxUnixMs.Store(time.Now().UnixMilli())
				h.emitPacket("SERIAL_OUT", buf[:n])
				if _, err := conn.Write(buf[:n]); err != nil {
					log.Printf("ser2net: %s write to udp client failed: %v", h.name, err)
					h.setRecentError(err)
				}
				h.emitPacket("UDP_OUT", buf[:n])
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
		h.writeMu.Lock()
		serial := h.serial
		h.writeMu.Unlock()
		if serial == nil {
			return
		}
		n, err := serial.Read(buf)
		if err != nil {
			if err == io.EOF {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			if isTimeout(err) {
				continue
			}
			log.Printf("ser2net: %s read serial error in udp server: %v", h.name, err)
			h.setRecentError(err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if n > 0 {
			h.bytesOut.Add(uint64(n))
			h.lastTxUnixMs.Store(time.Now().UnixMilli())
			h.emitPacket("SERIAL_OUT", buf[:n])
			data := make([]byte, n)
			copy(data, buf[:n])
			activePeers := peers.active()
			h.clients.Store(int32(len(activePeers)))
			for _, addr := range activePeers {
				if _, err := conn.WriteToUDP(data, addr); err != nil {
					log.Printf("ser2net: %s write to udp peer %s failed: %v", h.name, addr, err)
					h.setRecentError(err)
				}
			}
			h.emitPacket("UDP_OUT", buf[:n])
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
			if bytes.Equal(buf[:n], udpProbePayload) {
				continue
			}
			h.emitPacket("UDP_IN", buf[:n])
			if _, err := h.writeSerial(buf[:n]); err != nil {
				log.Printf("ser2net: %s write serial from udp server failed: %v", h.name, err)
				return
			}
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
