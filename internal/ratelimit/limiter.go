package ratelimit

import "golang.org/x/time/rate"

// GatewayLimiter controls connection count and bandwidth for gateway traffic.
// Injected into TunnelGateway; NopLimiter provides zero-overhead when disabled.
type GatewayLimiter interface {
	// AcquireConn reserves a connection slot (non-blocking, returns false if over limit).
	AcquireConn(nodeID, sKey string) bool

	// ReleaseConn frees a connection slot.
	ReleaseConn(nodeID, sKey string)

	// BWLimiterFor returns the bandwidth limiter for the given tunnel key, nil if unlimited.
	BWLimiterFor(sKey string) *rate.Limiter

	// UpdateTunnelConfig applies per-tunnel rate limit overrides at runtime.
	UpdateTunnelConfig(sKey string, cfg TunnelRateConfig)

	// RemoveTunnel cleans up rate limit state for an offline tunnel.
	RemoveTunnel(sKey string)

	// RemoveNode cleans up all rate limit state for a disconnected node.
	RemoveNode(nodeID string)
}

// NopLimiter is a zero-overhead no-op implementation.
type NopLimiter struct{}

func (NopLimiter) AcquireConn(_, _ string) bool         { return true }
func (NopLimiter) ReleaseConn(_, _ string)              {}
func (NopLimiter) BWLimiterFor(_ string) *rate.Limiter  { return nil }
func (NopLimiter) UpdateTunnelConfig(_ string, _ TunnelRateConfig) {}
func (NopLimiter) RemoveTunnel(_ string)                {}
func (NopLimiter) RemoveNode(_ string)                  {}
