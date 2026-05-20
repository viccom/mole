package ratelimit

import (
	"log/slog"
	"strings"
	"sync"

	"golang.org/x/time/rate"
)

// gatewayLimiterImpl implements GatewayLimiter with connection counting and bandwidth control.
type gatewayLimiterImpl struct {
	connMu         sync.Mutex
	nodeConns      map[string]int64 // nodeID -> active connection count
	tunnelConns    map[string]int64 // sKey -> active connection count
	tunnelOverrides map[string]connOverride

	maxConnsPerNode   int64
	maxConnsPerTunnel int64

	bwMu       sync.Mutex
	bwLimiters map[string]*rate.Limiter
	bwOverrides map[string]int64 // sKey -> override bps
	defaultBPS int64
	bwBurst    int
}

type connOverride struct {
	maxConns int64
}

// NewGatewayLimiter creates a combined connection and bandwidth limiter.
func NewGatewayLimiter(cfg GatewayRateLimitConfig) *gatewayLimiterImpl {
	return &gatewayLimiterImpl{
		nodeConns:        make(map[string]int64),
		tunnelConns:      make(map[string]int64),
		tunnelOverrides:  make(map[string]connOverride),
		maxConnsPerNode:  int64(cfg.MaxConnsPerNode),
		maxConnsPerTunnel: int64(cfg.MaxConnsPerTunnel),
		bwLimiters:       make(map[string]*rate.Limiter),
		bwOverrides:      make(map[string]int64),
		defaultBPS:       cfg.MaxBPSPerTunnel,
		bwBurst:          cfg.BWBurst,
	}
}

func (g *gatewayLimiterImpl) AcquireConn(nodeID, sKey string) bool {
	g.connMu.Lock()
	defer g.connMu.Unlock()
	if !g.checkLimits(nodeID, sKey) {
		return false
	}
	g.nodeConns[nodeID]++
	g.tunnelConns[sKey]++
	return true
}

func (g *gatewayLimiterImpl) ReleaseConn(nodeID, sKey string) {
	g.connMu.Lock()
	g.nodeConns[nodeID]--
	g.tunnelConns[sKey]--
	if g.nodeConns[nodeID] <= 0 {
		delete(g.nodeConns, nodeID)
	}
	if g.tunnelConns[sKey] <= 0 {
		delete(g.tunnelConns, sKey)
	}
	g.connMu.Unlock()
}

func (g *gatewayLimiterImpl) BWLimiterFor(sKey string) *rate.Limiter {
	bps := g.effectiveBPS(sKey)
	if bps <= 0 {
		return nil
	}
	g.bwMu.Lock()
	defer g.bwMu.Unlock()
	if l, ok := g.bwLimiters[sKey]; ok {
		return l
	}
	burst := g.bwBurst
	if burst <= 0 {
		burst = int(bps) // default burst = 1 second of bandwidth
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
	if cfg.MaxBPS > 0 {
		g.bwOverrides[sKey] = cfg.MaxBPS
	} else {
		delete(g.bwOverrides, sKey)
	}
	if l, ok := g.bwLimiters[sKey]; ok {
		bps := g.effectiveBPSLocked(sKey)
		if bps > 0 {
			l.SetLimit(rate.Limit(bps))
		}
	}
	g.bwMu.Unlock()
}

func (g *gatewayLimiterImpl) RemoveTunnel(sKey string) {
	g.connMu.Lock()
	delete(g.tunnelConns, sKey)
	delete(g.tunnelOverrides, sKey)
	g.connMu.Unlock()

	g.bwMu.Lock()
	delete(g.bwLimiters, sKey)
	delete(g.bwOverrides, sKey)
	g.bwMu.Unlock()
}

func (g *gatewayLimiterImpl) RemoveNode(nodeID string) {
	g.connMu.Lock()
	delete(g.nodeConns, nodeID)
	// Clean tunnel-level entries belonging to this node
	for sKey := range g.tunnelConns {
		if strings.HasPrefix(sKey, nodeID+"/") {
			delete(g.tunnelConns, sKey)
			delete(g.tunnelOverrides, sKey)
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
	if g.maxConnsPerNode > 0 {
		if g.nodeConns[nodeID] >= g.maxConnsPerNode {
			slog.Debug("Node connection limit exceeded", "nodeId", nodeID, "current", g.nodeConns[nodeID], "limit", g.maxConnsPerNode)
			return false
		}
	}

	// Tunnel-level check
	limit := g.maxConnsPerTunnel
	if override, ok := g.tunnelOverrides[sKey]; ok && override.maxConns > 0 {
		limit = override.maxConns
	}
	if limit > 0 {
		if g.tunnelConns[sKey] >= limit {
			slog.Debug("Tunnel connection limit exceeded", "sKey", sKey, "current", g.tunnelConns[sKey], "limit", limit)
			return false
		}
	}

	return true
}

func (g *gatewayLimiterImpl) effectiveBPS(sKey string) int64 {
	g.bwMu.Lock()
	defer g.bwMu.Unlock()
	return g.effectiveBPSLocked(sKey)
}

func (g *gatewayLimiterImpl) effectiveBPSLocked(sKey string) int64 {
	if override, ok := g.bwOverrides[sKey]; ok {
		return override
	}
	return g.defaultBPS
}
