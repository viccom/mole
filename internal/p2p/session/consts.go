//go:build p2p

package session

import (
	"net"
	"time"
)

// Time constants for session heartbeat and read timeouts.
const (
	HeartbeatFreq = 10 * time.Second
	ReadTimeout   = 35 * time.Second
)

// Outcome is what a successful connection mode produces; the session layer
// wraps it into a Session. 统一 secure 后端：engine mode 打洞后调 secureUpgrade
// （secure 协商 + yamux）产出 StreamMux，session 层只消费它（不碰 secureUpgrade，
// 避免 session→engine 循环依赖）。
type Outcome struct {
	Mux      StreamMux // engine 经 secureUpgrade 产出的多路复用 session
	IsClient bool      // mainStream 配对：true=Open(主动开)，false=Accept(被动收)
	Local    net.Addr
	Remote   net.Addr
}
