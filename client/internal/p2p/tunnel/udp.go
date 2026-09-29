//go:build p2p

package tunnel

import (
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

// clientTTL is how long we remember a client's address for response routing.
const clientTTL = 30 * time.Second

// udpForwardConcurrency 限制 acceptor 侧 per-frame 并发 socket 数（H-NEW-4：防 FD 耗尽）。
// 超出时丢弃新帧（UDP best-effort，丢弃优于 FD 耗尽致新连接失败）。
const udpForwardConcurrency = 256

// udpForwardReadTimeout 是 per-frame socket 首响应读超时（原 30s 过长；DNS/游戏响应 <1s）。
// 缩短也使 stop() 后 in-flight socket 在有界时间内自然回收。
const udpForwardReadTimeout = 5 * time.Second

type clientAddr struct {
	addr     net.Addr
	lastSeen time.Time
}

// SetupUDPListener 同步完成 ListenUDP + OpenStream + 写 header（initiator 侧）。
// 返回 ln+st 供 ServeUDPListener 使用。失败返回 err——C4：让 CreateTunnel 见到
// 启动错误（端口占用 / stream 耗尽 / 写失败），不再被 go 吞掉假装成功。
func SetupUDPListener(t *Tunnel, mux StreamOpener) (*net.UDPConn, net.Conn, error) {
	addr := fmt.Sprintf(":%d", t.Params.LocalPort)
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve udp :%d: %w", t.Params.LocalPort, err)
	}
	ln, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, nil, fmt.Errorf("listen udp :%d: %w", t.Params.LocalPort, err)
	}
	// H2：持锁 set closer + 检测 done，防止 stop 在 set 前跑（closer==nil 漏关 → 孤儿 listener）
	t.mu.Lock()
	select {
	case <-t.done:
		t.mu.Unlock()
		ln.Close()
		return nil, nil, fmt.Errorf("tunnel %s closed before listener bound", t.StringID())
	default:
		t.closer = ln
		t.mu.Unlock()
	}
	log.Printf("[tunnel] %s listening on UDP :%d", t.StringID(), t.Params.LocalPort)

	st, err := mux.OpenStream()
	if err != nil {
		ln.Close()
		return nil, nil, fmt.Errorf("open udp stream: %w", err)
	}
	header := make([]byte, 8)
	putUint32(header[0:4], UDPMagic)
	putUint32(header[4:8], t.ID)
	if _, err := st.Write(header); err != nil {
		st.Close()
		ln.Close()
		return nil, nil, fmt.Errorf("write udp header: %w", err)
	}
	return ln, st, nil
}

// ServeUDPListener 跑 UDP 隧道的读写循环，阻塞到 t.done。C3：track st 使 stop
// 能中断活跃转发（不留僵尸 stream）。
func ServeUDPListener(t *Tunnel, ln *net.UDPConn, st net.Conn) {
	t.trackConn(st)
	defer t.untrackConn(st)

	// Client address registry for response routing
	var clientsMu sync.Mutex
	clients := make(map[uint32]*clientAddr)

	// Cleanup goroutine
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-t.done:
				return
			case <-ticker.C:
				clientsMu.Lock()
				now := time.Now()
				for k, v := range clients {
					if now.Sub(v.lastSeen) > clientTTL {
						delete(clients, k)
					}
				}
				clientsMu.Unlock()
			}
		}
	}()

	// Read from local UDP clients → write to mux stream
	go func() {
		defer st.Close()
		defer ln.Close()
		buf := make([]byte, udpMaxData)
		for {
			n, from, err := ln.ReadFrom(buf)
			if err != nil {
				select {
				case <-t.done:
					return
				default:
					log.Printf("[tunnel] %s udp read: %v", t.StringID(), err)
					return
				}
			}
			key := sessionKey(from.String())
			clientsMu.Lock()
			clients[key] = &clientAddr{addr: from, lastSeen: time.Now()}
			clientsMu.Unlock()

			// Frame: [tunnelID 4B][sessionID 4B][dataLen 2B][data N bytes]
			frame := make([]byte, udpHeaderSize+n)
			putUint32(frame[0:4], t.ID)
			putUint32(frame[4:8], key)
			putUint16(frame[8:10], uint16(n))
			copy(frame[udpHeaderSize:], buf[:n])

			if _, err := st.Write(frame); err != nil {
				log.Printf("[tunnel] %s udp write: %v", t.StringID(), err)
				return
			}
			t.addOut(uint64(n))
		}
	}()

	// Read from mux stream → write to local UDP clients
	go func() {
		buf := make([]byte, udpHeaderSize+udpMaxData)
		for {
			// Read frame header
			if _, err := io.ReadFull(st, buf[:udpHeaderSize]); err != nil {
				select {
				case <-t.done:
					return
				default:
					log.Printf("[tunnel] %s udp recv: %v", t.StringID(), err)
					return
				}
			}
			dataLen := int(u16(buf[8:10]))
			if dataLen > udpMaxData {
				// Drain the declared length so the stream stays framed.
				// Without this, the next ReadFull would parse payload bytes
				// as a frame header and permanently desync the stream.
				if _, err := io.CopyN(io.Discard, st, int64(dataLen)); err != nil {
					log.Printf("[tunnel] %s udp drain: %v", t.StringID(), err)
					return
				}
				log.Printf("[tunnel] %s udp dropped oversized frame (%d bytes)", t.StringID(), dataLen)
				continue
			}
			if _, err := io.ReadFull(st, buf[udpHeaderSize:udpHeaderSize+dataLen]); err != nil {
				log.Printf("[tunnel] %s udp recv data: %v", t.StringID(), err)
				return
			}
			key := u32(buf[4:8])

			clientsMu.Lock()
			c, ok := clients[key]
			clientsMu.Unlock()
			if !ok {
				continue // client gone, drop
			}

			if _, err := ln.WriteTo(buf[udpHeaderSize:udpHeaderSize+dataLen], c.addr); err != nil {
				log.Printf("[tunnel] %s udp writeTo: %v", t.StringID(), err)
			}
			t.addIn(uint64(dataLen))
		}
	}()

	<-t.done
}

// handleUDPStream handles an incoming UDP data stream (acceptor side).
//
// Per-request UDP socket: each incoming frame from the mux stream gets its
// own short-lived connected UDP socket. This isolates request/response
// streams so concurrent clients (multiple LAN hosts hitting the same DNS
// tunnel) don't have their responses routed to whichever client happened
// to send last.
//
// The write to the mux stream is serialised through stMu because mux
// streams are not safe for concurrent Write.
func handleUDPStream(t *Tunnel, st net.Conn, _ bool) {
	defer st.Close()
	// C3：注册 st，使 stop 能中断活跃转发
	t.trackConn(st)
	defer t.untrackConn(st)

	target := net.JoinHostPort(t.Params.TargetHost, fmt.Sprint(t.Params.TargetPort))
	remoteAddr, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		log.Printf("[tunnel] %s resolve %s: %v", t.StringID(), target, err)
		return
	}

	var stMu sync.Mutex
	writeFrame := func(key uint32, payload []byte) error {
		frame := make([]byte, udpHeaderSize+len(payload))
		putUint32(frame[0:4], t.ID)
		putUint32(frame[4:8], key)
		putUint16(frame[8:10], uint16(len(payload)))
		copy(frame[udpHeaderSize:], payload)
		stMu.Lock()
		defer stMu.Unlock()
		if _, err := st.Write(frame); err != nil {
			return fmt.Errorf("udp tunnel write: %w", err)
		}
		return nil
	}

	done := make(chan struct{})
	// H-NEW-4：信号量限 per-frame 并发 socket 数，防高并发（如 DNS forwarding）FD 耗尽。
	sem := make(chan struct{}, udpForwardConcurrency)
	go func() {
		defer close(done)
		buf := make([]byte, udpHeaderSize+udpMaxData)
		for {
			if _, err := io.ReadFull(st, buf[:udpHeaderSize]); err != nil {
				return
			}
			dataLen := int(u16(buf[8:10]))
			if dataLen > udpMaxData {
				// Drain to keep the stream framed (see initiator-side comment).
				if _, err := io.CopyN(io.Discard, st, int64(dataLen)); err != nil {
					return
				}
				continue
			}
			if _, err := io.ReadFull(st, buf[udpHeaderSize:udpHeaderSize+dataLen]); err != nil {
				return
			}
			key := u32(buf[4:8])
			payload := make([]byte, dataLen)
			copy(payload, buf[udpHeaderSize:udpHeaderSize+dataLen])
			t.addIn(uint64(dataLen))

			// 并发上限：满则丢弃该帧（UDP best-effort，丢弃优于 FD 耗尽）。
			select {
			case sem <- struct{}{}:
			default:
				log.Printf("[tunnel] %s udp forward concurrency cap reached, drop frame (key=%d)", t.StringID(), key)
				continue
			}
			// Handle this request in its own goroutine with its own socket.
			go func(key uint32, payload []byte) {
				defer func() { <-sem }()
				sock, err := net.DialUDP("udp", nil, remoteAddr)
				if err != nil {
					log.Printf("[tunnel] %s udp dial: %v", t.StringID(), err)
					return
				}
				defer sock.Close()
				if _, err := sock.Write(payload); err != nil {
					log.Printf("[tunnel] %s udp send: %v", t.StringID(), err)
					return
				}
				sock.SetReadDeadline(time.Now().Add(udpForwardReadTimeout))
				resp := make([]byte, udpMaxData)
				for {
					n, err := sock.Read(resp)
					if err != nil {
						return // timeout or closed; give up on this request
					}
					if werr := writeFrame(key, resp[:n]); werr != nil {
						log.Printf("[tunnel] %s udp reply: %v", t.StringID(), werr)
						return
					}
					t.addOut(uint64(n))
					// UDP request/response typically single packet; keep
					// reading briefly for follow-up fragments.
					sock.SetReadDeadline(time.Now().Add(2 * time.Second))
				}
			}(key, payload)
		}
	}()

	select {
	case <-done:
	case <-t.done:
	}
}
