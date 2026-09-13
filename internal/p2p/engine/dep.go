//go:build p2p

// Package engine orchestrates p2punch's connection modes. Each mode calls
// into internal/easyp2p to punch a hole, then secure-upgrades the conn and
// returns a session.Outcome (StreamMux) + ECDHE shared key for session to wrap.
package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"moleAgent_client/internal/p2p/crypto"
	"moleAgent_client/internal/p2p/easyp2p"
	"moleAgent_client/internal/p2p/session"
)

// Time constants (mirrored from session for mode-level timeouts).
const (
	HeartbeatFreq = 10 * time.Second
	ReadTimeout   = 35 * time.Second
	RetryDelay    = 15 * time.Second
)

// DefaultModes is the try-order when -modes is omitted:
//
//	lan → tcp-v6 → udp-v6 → udp-v4
//
// tcp-v4 不在默认链：国内成功率 <10%，且其 TCP SYN 随机端口爆破（600×3）易触发
// 运营商/路由器端口扫描检测致本地网络瘫痪。需要时可手选（AllModes 仍含 tcp-v4）。
//
// 所有 mode 打洞后统一经 secureUpgrade（DTLS+KCP for UDP / TLS for TCP）+ yamux。
var DefaultModes = []string{"lan", "tcp-v6", "udp-v6", "udp-v4"}

// AllModes is the set of valid mode names. v4-relay 手选（不在 DefaultModes）：
// 纯 P2P 优先，relay 仅在直连/打洞全失败时由用户显式启用（需 -relay 配置 relaysrv 地址）。
var AllModes = map[string]bool{
	"lan": true, "tcp-v6": true, "udp-v6": true, "udp-v4": true, "tcp-v4": true,
	"v4-relay": true,
}

// Deps holds mode-function dependencies.
//
// 注意：STUN/MQTT 服务器列表不在 Deps 里。easyp2p 的 Easy_P2P_MP 不接收服务器参数，
// 内部直接读包级全局 easyp2p.STUNServers / MQTTBrokerServers。engine 经 SetServers() 在调
// mode 前注入 effective 列表（用户偏好前置 + 默认兜底，去重）——这是唯一让前端填的服务器
// 生效的路径（connection.go runConnection 调一次）。
type Deps struct {
	Room        string   // PSK for MQTT signaling + ECDHE context
	Modes       []string // parsed mode chain
	RelayServer string   // v4-relay mode 用：relaysrv TCP 地址（host:port）；空=不启用，ModeV4Relay 返 err
	ListenPort  int      // 0 = random
	Verbose     bool
	OnModeLog   func(string) // 可选：mode/secure 进度日志按行转发（桌面 UI modeLog 事件）；nil = 丢弃
}

// DefaultSTUNServers 返回 easyp2p 内置 STUN 列表（供前端展示 + connection.go 合并兜底）。
// 同步自 internal/easyp2p/stun.go 的 STUNServers。
func DefaultSTUNServers() []string {
	s := make([]string, len(easyp2p.STUNServers))
	copy(s, easyp2p.STUNServers)
	return s
}

// DefaultMQTTBrokers 返回 easyp2p 内置 MQTT broker 列表。
// 同步自 internal/easyp2p/p2p.go 的 MQTTBrokerServers。
func DefaultMQTTBrokers() []string {
	s := make([]string, len(easyp2p.MQTTBrokerServers))
	copy(s, easyp2p.MQTTBrokerServers)
	return s
}

// SetServers 把 effective STUN/MQTT 列表注入 easyp2p 包级全局。
// easyp2p.Easy_P2P_MP 内部读全局 STUNServers/MQTTBrokerServers（不接收参数），故这是让
// 前端填的服务器真正生效的唯一入口。非空才覆盖 easyp2p 内置默认；空则保留默认。
// 单连接场景（cli/desktop 同时只有一个 runConnection）无并发。
func SetServers(stun, mqtt []string) {
	if len(stun) > 0 {
		easyp2p.STUNServers = stun
	}
	if len(mqtt) > 0 {
		easyp2p.MQTTBrokerServers = mqtt
	}
}

// ModeFunc establishes a P2P connection using one strategy. Returns a
// session.Outcome (含 secureUpgrade 产出的 StreamMux) plus the ECDHE-derived
// shared key（作 secure PSK 来源）。
type ModeFunc func(ctx context.Context, deps Deps) (*session.Outcome, *[crypto.KeyLen]byte, error)

// Registry maps mode names to implementations. Populated by mode_*.go via
// init() so dep.go has no forward-dependency on not-yet-written modes.
var Registry = map[string]ModeFunc{}

// ParseModes parses the -modes flag.
//
//	""             → DefaultModes
//	"lan"          → [lan]
//	"lan,udp-v4"   → [lan udp-v4]
//	"v4-relay"     → [v4-relay]（手选；不在 DefaultModes，需配 -relay）
//
// Errors on unknown or duplicate mode names. v4-relay 手选（不在 DefaultModes）。
func ParseModes(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return DefaultModes, nil
	}
	parts := strings.Split(s, ",")
	var modes []string
	seen := map[string]bool{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !AllModes[p] {
			return nil, fmt.Errorf("unknown mode %q (valid: lan, tcp-v6, udp-v6, udp-v4, tcp-v4, v4-relay)", p)
		}
		if seen[p] {
			return nil, fmt.Errorf("duplicate mode %q", p)
		}
		seen[p] = true
		modes = append(modes, p)
	}
	if len(modes) == 0 {
		return DefaultModes, nil
	}
	return modes, nil
}
