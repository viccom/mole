//go:build p2p

package engine

func init() {
	// Register all modes. dep.go declares the empty map; this init populates
	// it so dep.go has no forward-dependency on the mode files.
	Registry["lan"] = ModeLAN
	Registry["tcp-v6"] = ModeV6TCP
	Registry["udp-v6"] = ModeV6UDP
	Registry["udp-v4"] = ModeV4UDP
	Registry["tcp-v4"] = ModeV4TCP
	Registry["v4-relay"] = ModeV4Relay
}
