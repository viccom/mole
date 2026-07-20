package ratelimit

import (
	"log/slog"
	"strings"
	"sync"

	"golang.org/x/time/rate"
)

// gatewayLimiterImpl implements GatewayLimiter with connection counting and bandwidth control.
type gatewayLimiterImpl struct {
	connMu          sync.Mutex
	nodeConns       map[string]int64 // nodeID -> active connection count
	tunnelConns     map[string]int64 // sKey -> active connection count
	tunnelOverrides map[string]connOverride
	nodeOverrides   map[string]connOverride // nodeID -> per-node MaxConns override
	tunnelGens      map[string]uint64       // sKey -> generation for ReleaseConn validation

	maxConnsPerNode   int64
	maxConnsPerTunnel int64

	bwMu       sync.Mutex
	bwLimiters map[string]*rate.Limiter
	bwOverrides map[string]int64 // sKey -> override bandwidth (bytes/sec)
	defaultBPS  int64
	bwBurst     int
}

type connOverride struct {
	maxConns int64
}

// NewGatewayLimiter creates a combined connection and bandwidth limiter.
// When Enabled is false the caller should not create a limiter and use NopLimiter instead.
func NewGatewayLimiter(cfg GatewayRateLimitConfig) *gatewayLimiterImpl {
	return &gatewayLimiterImpl{
		nodeConns:        make(map[string]int64),
		tunnelConns:      make(map[string]int64),
		tunnelOverrides:  make(map[string]connOverride),
		nodeOverrides:    make(map[string]connOverride),
		tunnelGens:       make(map[string]uint64),
		maxConnsPerNode:  int64(cfg.MaxConnsPerNode),
		maxConnsPerTunnel: int64(cfg.MaxConnsPerTunnel),
		bwLimiters:       make(map[string]*rate.Limiter),
		bwOverrides:      make(map[string]int64),
		defaultBPS:       cfg.MaxBandwidthPerTunnel,
		bwBurst:          cfg.BWBurst,
	}
}

func (g *gatewayLimiterImpl) AcquireConn(nodeID, sKey string) (bool, uint64) {
	g.connMu.Lock()
	defer g.connMu.Unlock()
	if !g.checkLimits(nodeID, sKey) {
		return false, 0
	}
	g.nodeConns[nodeID]++
	g.tunnelConns[sKey]++
	return true, g.tunnelGens[sKey]
}

func (g *gatewayLimiterImpl) ReleaseConn(nodeID, sKey string, gen uint64) {
	g.connMu.Lock()
	if g.tunnelGens[sKey] == gen {
		if n, ok := g.nodeConns[nodeID]; ok {
			if n <= 1 {
				delete(g.nodeConns, nodeID)
			} else {
				g.nodeConns[nodeID] = n - 1
			}
		}
		if n, ok := g.tunnelConns[sKey]; ok {
			if n <= 1 {
				delete(g.tunnelConns, sKey)
			} else {
				g.tunnelConns[sKey] = n - 1
			}
		}
	}
	g.connMu.Unlock()
}

func (g *gatewayLimiterImpl) BWLimiterFor(sKey string) *rate.Limiter {
	g.bwMu.Lock()
	defer g.bwMu.Unlock()
	if l, ok := g.bwLimiters[sKey]; ok {
		return l
	}
	bps := g.effectiveBPSLocked(sKey)
	if bps <= 0 {
		return nil
	}
	burst := g.bwBurst
	if burst <= 0 {
		burst = safeBurst(bps)
	}
	l := rate.NewLimiter(rate.Limit(bps), burst)
	g.bwLimiters[sKey] = l
	return l
}

func (g *gatewayLimiterImpl) UpdateTunnelConfig(sKey string, cfg TunnelRateConfig) {
	g.connMu.Lock()
	if cfg.MaxConns > 0 {
		g.tunnelOverrides[sKey] = connOverride{maxConns: int64(cfg.MaxConns)}
	} else {
		delete(g.tunnelOverrides, sKey)
	}
	g.connMu.Unlock()

	g.bwMu.Lock()
	if cfg.MaxBandwidth > 0 {
		g.bwOverrides[sKey] = cfg.MaxBandwidth
	} else {
		delete(g.bwOverrides, sKey)
	}
	if l, ok := g.bwLimiters[sKey]; ok {
		bps := g.effectiveBPSLocked(sKey)
		if bps > 0 {
			l.SetLimit(rate.Limit(bps))
			l.SetBurst(safeBurst(bps))
		} else {
			delete(g.bwLimiters, sKey)
		}
	}
	g.bwMu.Unlock()
}

func (g *gatewayLimiterImpl) UpdateNodeConfig(nodeID string, cfg NodeRateConfig) {
	g.connMu.Lock()
	if cfg.MaxConns > 0 {
		g.nodeOverrides[nodeID] = connOverride{maxConns: int64(cfg.MaxConns)}
	} else {
		delete(g.nodeOverrides, nodeID)
	}
	g.connMu.Unlock()
}

func (g *gatewayLimiterImpl) EffectiveLimits(nodeID, sKey string) EffectiveLimits {
	g.connMu.Lock()
	maxConns := g.effectiveNodeConns(nodeID)
	tunnelConns := g.effectiveTunnelConns(sKey)
	g.connMu.Unlock()

	g.bwMu.Lock()
	maxBW := g.effectiveBPSLocked(sKey)
	g.bwMu.Unlock()

	return EffectiveLimits{
		MaxConns:     int(minPositive(maxConns, tunnelConns)),
		MaxBandwidth: maxBW,
	}
}

func (g *gatewayLimiterImpl) GlobalDefaults() (int, int, int64) {
	return int(g.maxConnsPerNode), int(g.maxConnsPerTunnel), g.defaultBPS
}

func (g *gatewayLimiterImpl) RemoveTunnel(sKey string) {
	g.connMu.Lock()
	// 扣减该隧道占用的节点级连接计数：残留活跃连接的 ReleaseConn 会因 gen 自增被跳过，
	// 若不在此回扣，nodeConns 只增不减 → MaxConnsPerNode 被逐渐吃光 → 节点假死。
	if idx := strings.Index(sKey, "/"); idx > 0 {
		nodeID := sKey[:idx]
		if t := g.tunnelConns[sKey]; t > 0 {
			if n := g.nodeConns[nodeID] - t; n <= 0 {
				delete(g.nodeConns, nodeID)
			} else {
				g.nodeConns[nodeID] = n
			}
		}
	}
	delete(g.tunnelConns, sKey)
	delete(g.tunnelOverrides, sKey)
	g.tunnelGens[sKey]++
	g.connMu.Unlock()

	g.bwMu.Lock()
	delete(g.bwLimiters, sKey)
	delete(g.bwOverrides, sKey)
	g.bwMu.Unlock()
}

func (g *gatewayLimiterImpl) RemoveNode(nodeID string) {
	g.connMu.Lock()
	delete(g.nodeConns, nodeID)
	delete(g.nodeOverrides, nodeID)
	for sKey := range g.tunnelConns {
		if strings.HasPrefix(sKey, nodeID+"/") {
			delete(g.tunnelConns, sKey)
			delete(g.tunnelOverrides, sKey)
		}
	}
	for sKey := range g.tunnelGens {
		if strings.HasPrefix(sKey, nodeID+"/") {
			delete(g.tunnelGens, sKey)
		}
	}
	g.connMu.Unlock()

	g.bwMu.Lock()
	for sKey := range g.bwLimiters {
		if strings.HasPrefix(sKey, nodeID+"/") {
			delete(g.bwLimiters, sKey)
			delete(g.bwOverrides, sKey)
		}
	}
	g.bwMu.Unlock()
}

func (g *gatewayLimiterImpl) checkLimits(nodeID, sKey string) bool {
	// Node-level check
	nodeLimit := g.effectiveNodeConns(nodeID)
	if nodeLimit > 0 {
		if g.nodeConns[nodeID] >= nodeLimit {
			slog.Warn("Node connection limit exceeded", "nodeId", nodeID, "current", g.nodeConns[nodeID], "limit", nodeLimit)
			return false
		}
	}

	// Tunnel-level check
	tunnelLimit := g.effectiveTunnelConns(sKey)
	if tunnelLimit > 0 {
		if g.tunnelConns[sKey] >= tunnelLimit {
			slog.Warn("Tunnel connection limit exceeded", "sKey", sKey, "current", g.tunnelConns[sKey], "limit", tunnelLimit)
			return false
		}
	}

	return true
}

func (g *gatewayLimiterImpl) effectiveNodeConns(nodeID string) int64 {
	if override, ok := g.nodeOverrides[nodeID]; ok && override.maxConns > 0 {
		return override.maxConns
	}
	return g.maxConnsPerNode
}

func (g *gatewayLimiterImpl) effectiveTunnelConns(sKey string) int64 {
	if override, ok := g.tunnelOverrides[sKey]; ok && override.maxConns > 0 {
		return override.maxConns
	}
	return g.maxConnsPerTunnel
}

func (g *gatewayLimiterImpl) effectiveBPSLocked(sKey string) int64 {
	if override, ok := g.bwOverrides[sKey]; ok {
		return override
	}
	return g.defaultBPS
}

// safeBurst converts bps to a safe burst value, capping at MaxInt.
func safeBurst(bps int64) int {
	const maxInt = int64(^uint(0) >> 1)
	if bps > maxInt {
		return int(maxInt)
	}
	return int(bps)
}

func minPositive(a, b int64) int64 {
	if a <= 0 {
		return b
	}
	if b <= 0 {
		return a
	}
	if a < b {
		return a
	}
	return b
}
