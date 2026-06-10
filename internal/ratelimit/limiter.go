package ratelimit

import "golang.org/x/time/rate"

// EffectiveLimits returns the computed rate limits for a given node/tunnel pair.
type EffectiveLimits struct {
	MaxConns      int   // effective max concurrent connections (0 = unlimited)
	MaxBandwidth  int64 // effective bandwidth limit in bytes/sec (0 = unlimited)
}

// GatewayLimiter controls connection count and bandwidth for gateway traffic.
// Injected into TunnelGateway; NopLimiter provides zero-overhead when disabled.
type GatewayLimiter interface {
	// AcquireConn reserves a connection slot (non-blocking, returns false if over limit).
	// Returns the generation token for ReleaseConn validation.
	AcquireConn(nodeID, sKey string) (bool, uint64)

	// ReleaseConn frees a connection slot. gen must match the value returned by AcquireConn.
	ReleaseConn(nodeID, sKey string, gen uint64)

	// BWLimiterFor returns the bandwidth limiter for the given tunnel key, nil if unlimited.
	BWLimiterFor(sKey string) *rate.Limiter

	// UpdateTunnelConfig applies per-tunnel rate limit overrides at runtime.
	UpdateTunnelConfig(sKey string, cfg TunnelRateConfig)

	// UpdateNodeConfig applies per-node rate limit overrides at runtime.
	UpdateNodeConfig(nodeID string, cfg NodeRateConfig)

	// EffectiveLimits returns the effective limits for a node/tunnel pair.
	EffectiveLimits(nodeID, sKey string) EffectiveLimits

	// RemoveTunnel cleans up rate limit state for an offline tunnel.
	RemoveTunnel(sKey string)

	// RemoveNode cleans up all rate limit state for a disconnected node.
	RemoveNode(nodeID string)

	// GlobalDefaults returns the configured global defaults.
	GlobalDefaults() (maxConnsPerNode int, maxConnsPerTunnel int, maxBandwidthPerTunnel int64)
}

// NopLimiter is a zero-overhead no-op implementation.
type NopLimiter struct{}

func (NopLimiter) AcquireConn(_, _ string) (bool, uint64)                    { return true, 0 }
func (NopLimiter) ReleaseConn(_, _ string, _ uint64)                         {}
func (NopLimiter) BWLimiterFor(_ string) *rate.Limiter                       { return nil }
func (NopLimiter) UpdateTunnelConfig(_ string, _ TunnelRateConfig)           {}
func (NopLimiter) UpdateNodeConfig(_ string, _ NodeRateConfig)               {}
func (NopLimiter) EffectiveLimits(_, _ string) EffectiveLimits               { return EffectiveLimits{} }
func (NopLimiter) RemoveTunnel(_ string)                                     {}
func (NopLimiter) RemoveNode(_ string)                                       {}
func (NopLimiter) GlobalDefaults() (int, int, int64)                         { return 0, 0, 0 }
