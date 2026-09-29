//go:build p2p

package session

import "strings"

// ---- Heartbeat protocol ----
//
// All transports use the same wire format:
//
//	HB:<random>          heartbeat keep-alive (silent on success)
//	MSG:<text>           user text message
//
// Heartbeats are sent every HeartbeatFreq. They are NOT logged on success;
// only logged on the first consecutive failure (and recovery).

const (
	msgPrefix = "MSG:"
	hbPrefix  = "HB:"
)

func isHeartbeat(data string) bool { return strings.HasPrefix(data, hbPrefix) }

func isProtocolNoise(data string) bool {
	return data == "" || data == "PUNCH" || strings.HasPrefix(data, "TCP:")
}
