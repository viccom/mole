//go:build p2p

// Package tunnel provides TCP/UDP port mapping over P2P connections.
//
// Each tunnel maps a local listening port to a remote target (host:port).
// Control messages use a text-line protocol over the main heartbeat stream.
// Data flows over dedicated QUIC streams identified by magic numbers.
package tunnel

import "encoding/binary"

// ---- Magic numbers for QUIC stream dispatch ----

// TCPMagic identifies a TCP tunnel data stream ("TUNC").
const TCPMagic uint32 = 0x54554E43

// UDPMagic identifies a UDP tunnel data stream ("TUNU").
const UDPMagic uint32 = 0x54554E55

// ---- Control protocol (text-line over main stream) ----

// Control message prefixes.
const (
	ctrlOpen  = "TUNNEL:OPEN:"
	ctrlOK    = "TUNNEL:OK:"
	ctrlErr   = "TUNNEL:ERR:"
	ctrlClose = "TUNNEL:CLOSE:"
	ctrlCloseAck = "TUNNEL:CLOSED:"
)

// ---- Data frame layout ----

// TCP data frame: [tunnelID 4B][payload...]
// Runs as a raw byte stream after the initial header.

// UDP data frame: [tunnelID 4B][sessionID 4B][dataLen 2B][data N bytes]
// sessionID allows the initiator to route responses to the correct client.

const (
	udpHeaderSize = 4 + 4 + 2 // tunnelID + sessionID + dataLen
	udpMaxData    = 65507      // max UDP payload (65535 - IP header - UDP header)
)

// ---- Helpers ----

func putUint32(b []byte, v uint32) { binary.BigEndian.PutUint32(b, v) }
func putUint16(b []byte, v uint16) { binary.BigEndian.PutUint16(b, v) }
func u32(b []byte) uint32          { return binary.BigEndian.Uint32(b) }
func u16(b []byte) uint16          { return binary.BigEndian.Uint16(b) }

// sessionKey hashes a UDP client address into a 4-byte session identifier.
func sessionKey(addr string) uint32 {
	var h uint32 = 5381
	for i := 0; i < len(addr); i++ {
		h = ((h << 5) + h) + uint32(addr[i])
	}
	return h
}
