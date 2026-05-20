package ratelimit

import "testing"

func TestNopLimiter_AcquireReleaseConn(t *testing.T) {
	var l GatewayLimiter = NopLimiter{}
	if !l.AcquireConn("node1", "node1/t1") {
		t.Error("NopLimiter.AcquireConn should return true")
	}
	l.ReleaseConn("node1", "node1/t1")
}

func TestNopLimiter_BWLimiterFor(t *testing.T) {
	var l GatewayLimiter = NopLimiter{}
	if l.BWLimiterFor("node1/t1") != nil {
		t.Error("NopLimiter.BWLimiterFor should return nil")
	}
}

func TestNopLimiter_UpdateTunnelConfig(t *testing.T) {
	var l GatewayLimiter = NopLimiter{}
	l.UpdateTunnelConfig("node1/t1", TunnelRateConfig{MaxConns: 100})
}

func TestNopLimiter_RemoveTunnel(t *testing.T) {
	var l GatewayLimiter = NopLimiter{}
	l.RemoveTunnel("node1/t1")
}

func TestNopLimiter_RemoveNode(t *testing.T) {
	var l GatewayLimiter = NopLimiter{}
	l.RemoveNode("node1")
}
