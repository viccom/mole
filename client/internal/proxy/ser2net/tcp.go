package ser2net

import (
	"io"
	"log"
	"net"
	"sync"
	"time"
)

func (h *Handler) runTCPServer() {
	defer h.cleanup()

	ln, err := net.Listen("tcp", h.cfg.Address)
	if err != nil {
		log.Printf("ser2net: %s tcp server listen error: %v", h.name, err)
		h.setError(err)
		return
	}
	h.setListener(ln)
	defer ln.Close()
	defer h.setListener(nil)
	log.Printf("ser2net: %s tcp server listening on %s", h.name, h.cfg.Address)

	clients := newConnSet(h.maxConn())
	h.setTCPClients(clients)
	defer h.setTCPClients(nil)

	// Serial → all TCP clients
	go h.serialToTCPClients(clients)

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-h.ctx.Done():
				return
			default:
				log.Printf("ser2net: %s accept error: %v", h.name, err)
				h.setError(err)
				return
			}
		}
		clients.add(conn)
		h.clients.Store(int32(clients.len()))
		log.Printf("ser2net: %s client connected %s (total %d)", h.name, conn.RemoteAddr(), clients.len())

		go func(c net.Conn) {
			h.tcpClientToSerial(c, clients)
			clients.remove(c)
			c.Close()
			h.clients.Store(int32(clients.len()))
		}(conn)
	}
}

func (h *Handler) runTCPClient() {
	defer h.cleanup()

	for {
		select {
		case <-h.ctx.Done():
			return
		default:
		}

		conn, err := net.DialTimeout("tcp", h.cfg.Address, 10*time.Second)
		if err != nil {
			log.Printf("ser2net: %s tcp client connect error: %v", h.name, err)
			h.setTransientError(err)
			select {
			case <-h.ctx.Done():
				return
			case <-time.After(3 * time.Second):
				continue
			}
		}

		log.Printf("ser2net: %s tcp client connected to %s", h.name, h.cfg.Address)
		h.clearError()
		h.running.Store(true)
		h.clients.Store(1)
		h.relayTCPClient(conn)
		h.clients.Store(0)
		conn.Close()
		log.Printf("ser2net: %s tcp client disconnected, reconnecting...", h.name)

		select {
		case <-h.ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

func (h *Handler) relayTCPClient(conn net.Conn) {
	done := make(chan struct{})
	var once sync.Once
	closeDone := func() { once.Do(func() { close(done) }) }

	// TCP → Serial
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
				if err != io.EOF {
					log.Printf("ser2net: %s tcp client read error: %v", h.name, err)
				}
				return
			}
			if n > 0 {
				h.emitPacket("TCP_IN", buf[:n])
				if _, err := h.writeSerial(buf[:n]); err != nil {
					log.Printf("ser2net: %s write serial from tcp client failed: %v", h.name, err)
					return
				}
			}
		}
	}()

	// Serial → TCP
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
				log.Printf("ser2net: %s read serial error in tcp client: %v", h.name, err)
				h.setRecentError(err)
				time.Sleep(100 * time.Millisecond)
				continue
			}
			if n > 0 {
				h.bytesOut.Add(uint64(n))
				h.lastTxUnixMs.Store(time.Now().UnixMilli())
				h.emitPacket("SERIAL_OUT", buf[:n])
				conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if _, err := conn.Write(buf[:n]); err != nil {
					log.Printf("ser2net: %s write to tcp client failed: %v", h.name, err)
					return
				}
				h.emitPacket("TCP_OUT", buf[:n])
			}
		}
	}()
}

func (h *Handler) serialToTCPClients(clients *connSet) {
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
			log.Printf("ser2net: %s read serial error in tcp server: %v", h.name, err)
			h.setRecentError(err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if n > 0 {
			h.bytesOut.Add(uint64(n))
			h.lastTxUnixMs.Store(time.Now().UnixMilli())
			h.emitPacket("SERIAL_OUT", buf[:n])
			h.emitPacket("TCP_OUT", buf[:n])
			clients.broadcast(buf[:n])
		}
	}
}

func (h *Handler) tcpClientToSerial(conn net.Conn, clients *connSet) {
	buf := make([]byte, 4096)
	for {
		select {
		case <-h.ctx.Done():
			return
		default:
		}
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, err := conn.Read(buf)
		if err != nil {
			if isNetTimeout(err) {
				continue
			}
			if err != io.EOF {
				log.Printf("ser2net: %s client %s read error: %v", h.name, conn.RemoteAddr(), err)
			}
			return
		}
		if n > 0 {
			h.emitPacket("TCP_IN", buf[:n])
			if _, err := h.writeSerial(buf[:n]); err != nil {
				log.Printf("ser2net: %s write serial from tcp server failed: %v", h.name, err)
				return
			}
		}
	}
}

func isNetTimeout(err error) bool {
	if err == nil {
		return false
	}
	if ne, ok := err.(interface{ Timeout() bool }); ok && ne.Timeout() {
		return true
	}
	return false
}
