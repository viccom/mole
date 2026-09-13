//go:build p2p

package tunnel

import (
	"fmt"
	"io"
	"log"
	"net"
)

// StartTCPListener starts a TCP listener for the tunnel (initiator side).
//
// For each accepted connection, opens a new mux stream and bridges
// the local TCP connection to the remote peer through it.
// Blocks until t.done is closed.
func StartTCPListener(t *Tunnel, mux StreamOpener) error {
	addr := fmt.Sprintf(":%d", t.Params.LocalPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen tcp :%d: %w", t.Params.LocalPort, err)
	}
	// H2：持锁 set closer + 检测 done，防止 stop 在 set 前跑（closer==nil 漏关 → 孤儿 listener）
	t.mu.Lock()
	select {
	case <-t.done:
		t.mu.Unlock()
		ln.Close()
		return fmt.Errorf("tunnel %s closed before listener bound", t.StringID())
	default:
		t.closer = ln
		t.mu.Unlock()
	}
	log.Printf("[tunnel] %s listening on :%d", t.StringID(), t.Params.LocalPort)

	go func() {
		defer ln.Close()
		for {
			localConn, err := ln.Accept()
			if err != nil {
				select {
				case <-t.done:
					return
				default:
					log.Printf("[tunnel] %s accept: %v", t.StringID(), err)
					return
				}
			}
			go t.bridgeTCP(mux, localConn.(*net.TCPConn))
		}
	}()
	return nil
}

// bridgeTCP opens a new mux stream and bridges the local TCP connection
// to the remote peer.
func (t *Tunnel) bridgeTCP(mux StreamOpener, local *net.TCPConn) {
	st, err := mux.OpenStream()
	if err != nil {
		log.Printf("[tunnel] %s open stream: %v", t.StringID(), err)
		local.Close()
		return
	}
	// C3：注册 st+local，使 stop 能中断活跃转发（不留僵尸连接）
	t.trackConn(st)
	t.trackConn(local)
	defer t.untrackConn(st)
	defer t.untrackConn(local)

	// Write header: magic + tunnel ID
	header := make([]byte, 8)
	putUint32(header[0:4], TCPMagic)
	putUint32(header[4:8], t.ID)
	if _, err := st.Write(header); err != nil {
		log.Printf("[tunnel] %s write header: %v", t.StringID(), err)
		st.Close()
		local.Close()
		return
	}

	// C-NEW-1：去掉多余的内层 go func——bridgeTCP 由调用者 go 起跑（tcp.go accept 循环），
	// 此处直接阻塞到 copy 结束，使 defer untrackConn 在转发结束后才触发，stop 才能关活跃 st/local。
	// 原内层 go func 致函数立即 return、defer 提前触发，C3 在 initiator-TCP 分支失效。
	defer st.Close()
	defer local.Close()
	done := make(chan struct{}, 2)
	go func() {
		n, _ := io.Copy(st, local)
		t.addOut(uint64(n))
		done <- struct{}{}
	}()
	go func() {
		n, _ := io.Copy(local, st)
		t.addIn(uint64(n))
		done <- struct{}{}
	}()
	<-done
}

// handleTCPStream handles an incoming TCP data stream (acceptor side).
func handleTCPStream(t *Tunnel, st net.Conn) {
	defer st.Close()

	target := net.JoinHostPort(t.Params.TargetHost, fmt.Sprint(t.Params.TargetPort))
	remote, err := net.Dial("tcp", target)
	if err != nil {
		log.Printf("[tunnel] %s dial %s: %v", t.StringID(), target, err)
		return
	}
	// C3：注册 st+remote，使 stop 能中断活跃转发
	t.trackConn(st)
	t.trackConn(remote)
	defer t.untrackConn(st)
	defer t.untrackConn(remote)
	defer remote.Close()

	// Bidirectional copy between remote target and QUIC stream
	done := make(chan struct{}, 2)
	go func() {
		n, _ := io.Copy(st, remote)
		t.addOut(uint64(n))
		done <- struct{}{}
	}()
	go func() {
		n, _ := io.Copy(remote, st)
		t.addIn(uint64(n))
		done <- struct{}{}
	}()
	<-done
}
