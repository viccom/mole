//go:build p2p

// Package transport provides TCP/UDP socket tuning helpers.
// (QUIC helpers removed — all transports now go through internal/secure.)
package transport

import (
	"log"
	"net"
	"time"
)

// TuneUDPBuffers increases UDP socket buffer to 16 MB.
//
// Windows default is only 64 KB, far below BDP (~6 MB for 1 Gbps × 50 ms RTT).
// 16 MB exceeds BDP, preventing kernel drops during high-speed transfers.
func TuneUDPBuffers(c *net.UDPConn) {
	c.SetReadBuffer(16 * 1024 * 1024)
	c.SetWriteBuffer(16 * 1024 * 1024)
}

// ClearUDPDeadlines removes any residual read/write deadlines on the socket.
//
// Critical invariant: hole-punch phase sets 1s ReadDeadline; secure/KCP takeover
// must clear it first or reads will time out immediately.
func ClearUDPDeadlines(c *net.UDPConn) {
	c.SetReadDeadline(time.Time{})
	c.SetWriteDeadline(time.Time{})
}

// TuneTCPConn tunes a TCP connection: 4 MB send/recv buffer + NoDelay.
//
// Windows default SO_SNDBUF/SO_RCVBUF ≈ 8 KB, far below BDP (~6 MB),
// causing sender to stall waiting for ACK. 4 MB is enough for gigabit links.
func TuneTCPConn(c *net.TCPConn) {
	if err := c.SetWriteBuffer(4 * 1024 * 1024); err != nil {
		log.Printf("[tcp] set write buffer: %v (using OS default)", err)
	}
	if err := c.SetReadBuffer(4 * 1024 * 1024); err != nil {
		log.Printf("[tcp] set read buffer: %v (using OS default)", err)
	}
	if err := c.SetNoDelay(true); err != nil {
		log.Printf("[tcp] set nodelay: %v", err)
	}
}
