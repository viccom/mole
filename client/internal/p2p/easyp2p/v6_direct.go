//go:build p2p

package easyp2p

import (
	"context"
	"fmt"
	"io"
	"time"
)

// IPv6DirectPayload is exchanged over MQTT for IPv6 direct connection.
// IPv6 has no NAT, so each peer publishes ALL its globally-reachable IPv6
// socket addresses (GUAs) + an ECDHE public key. The peer concurrently dials
// every address and keeps the first that succeeds.
type IPv6DirectPayload struct {
	Addrs  []string `json:"addrs"` // all local GUA socket addresses, "[ipv6]:port"
	PubKey string   `json:"pk"`    // ECDHE public key, base64 (uncompressed)
}

// ExchangeIPv6Address exchanges IPv6 direct-connect info with the peer over MQTT.
// Uses a dedicated topic salt "p2punch-v6" so it never collides with gonc's
// NAT-exchange topics. Returns the peer's payload.
//
// This bypasses gonc's STUN-based DetectNATAddressInfo (which fails for udp6:
// public STUN servers are IPv4-only, so udp6 STUN dial fails and no address
// is ever exchanged). IPv6 needs no STUN — the local GUA is the public addr.
func ExchangeIPv6Address(ctx context.Context, room string, local *IPv6DirectPayload, timeout time.Duration, logWriter io.Writer) (*IPv6DirectPayload, error) {
	signal, err := NewMQTTSignalSession(ctx, MQTT_GenerateClientID(TopicDesc_Signal, room, 0), "", logWriter)
	if err != nil {
		return nil, fmt.Errorf("ipv6 mqtt: %w", err)
	}
	defer signal.Close()
	if err := signal.prepareTopic(ctx, "p2punch-v6", room); err != nil {
		return nil, fmt.Errorf("ipv6 mqtt topic: %w", err)
	}
	topicCID := MQTT_GenerateClientID(TopicDesc_Signal, room, 0)
	remote, _, err := MQTT_SecureExchangeWithSession[IPv6DirectPayload](
		ctx, signal, EXMODE_mutual, local, topicCID, "p2punch-v6", room, timeout, nil)
	if err != nil {
		return nil, fmt.Errorf("ipv6 exchange: %w", err)
	}
	return &remote, nil
}
