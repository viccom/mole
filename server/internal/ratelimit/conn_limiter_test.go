package ratelimit

import (
	"sync"
	"testing"
	"time"
)

func TestGatewayLimiter_AcquireReleaseConn(t *testing.T) {
	cfg := GatewayRateLimitConfig{
		MaxConnsPerNode:   2,
		MaxConnsPerTunnel: 1,
	}
	g := NewGatewayLimiter(cfg)

	ok, gen := g.AcquireConn("n1", "n1/t1")
	if !ok {
		t.Error("first acquire should succeed")
	}
	if ok, _ := g.AcquireConn("n1", "n1/t1"); ok {
		t.Error("second acquire for same tunnel should fail (limit=1)")
	}
	if ok, _ := g.AcquireConn("n1", "n1/t2"); !ok {
		t.Error("different tunnel should succeed")
	}
	if ok, _ := g.AcquireConn("n1", "n1/t3"); ok {
		t.Error("third connection for same node should fail (limit=2)")
	}

	g.ReleaseConn("n1", "n1/t1", gen)
	if ok, _ := g.AcquireConn("n1", "n1/t1"); !ok {
		t.Error("acquire after release should succeed")
	}
}

func TestGatewayLimiter_UnlimitedWhenZero(t *testing.T) {
	cfg := GatewayRateLimitConfig{} // all zeros = unlimited
	g := NewGatewayLimiter(cfg)

	for i := range 100 {
		sKey := "n1/t" + string(rune(i))
		if ok, _ := g.AcquireConn("n1", sKey); !ok {
			t.Errorf("acquire %d should succeed with unlimited config", i)
		}
	}
}

func TestGatewayLimiter_NodeLimit(t *testing.T) {
	cfg := GatewayRateLimitConfig{
		MaxConnsPerNode:   3,
		MaxConnsPerTunnel: 0, // unlimited per tunnel
	}
	g := NewGatewayLimiter(cfg)

	for i := range 3 {
		sKey := "n1/t" + string(rune(i))
		if ok, _ := g.AcquireConn("n1", sKey); !ok {
			t.Errorf("acquire %d should succeed", i)
		}
	}
	if ok, _ := g.AcquireConn("n1", "n1/t4"); ok {
		t.Error("4th connection should fail (node limit=3)")
	}

	// Different node should work
	if ok, _ := g.AcquireConn("n2", "n2/t1"); !ok {
		t.Error("different node should not be affected")
	}
}

func TestGatewayLimiter_TunnelOverride(t *testing.T) {
	cfg := GatewayRateLimitConfig{
		MaxConnsPerTunnel: 10,
	}
	g := NewGatewayLimiter(cfg)
	g.UpdateTunnelConfig("n1/t1", TunnelRateConfig{MaxConns: 1})

	if ok, _ := g.AcquireConn("n1", "n1/t1"); !ok {
		t.Error("first acquire should succeed")
	}
	if ok, _ := g.AcquireConn("n1", "n1/t1"); ok {
		t.Error("second acquire should fail (override limit=1)")
	}

	// Different tunnel should use default
	if ok, _ := g.AcquireConn("n1", "n1/t2"); !ok {
		t.Error("different tunnel should use default limit")
	}
}

func TestGatewayLimiter_BWLimiterFor(t *testing.T) {
	cfg := GatewayRateLimitConfig{
		MaxBandwidthPerTunnel: 1024,
		BWBurst:         512,
	}
	g := NewGatewayLimiter(cfg)

	l := g.BWLimiterFor("n1/t1")
	if l == nil {
		t.Error("should return a limiter when BPS > 0")
	}
	l2 := g.BWLimiterFor("n1/t1")
	if l != l2 {
		t.Error("should return same limiter for same key")
	}
}

func TestGatewayLimiter_BWLimiterUnlimited(t *testing.T) {
	cfg := GatewayRateLimitConfig{MaxBandwidthPerTunnel: 0}
	g := NewGatewayLimiter(cfg)

	l := g.BWLimiterFor("n1/t1")
	if l != nil {
		t.Error("should return nil when BPS=0")
	}
}

func TestGatewayLimiter_RemoveTunnel(t *testing.T) {
	cfg := GatewayRateLimitConfig{
		MaxConnsPerTunnel: 1,
		MaxBandwidthPerTunnel:   1024,
	}
	g := NewGatewayLimiter(cfg)

	_, gen := g.AcquireConn("n1", "n1/t1")
	g.ReleaseConn("n1", "n1/t1", gen)
	_ = g.BWLimiterFor("n1/t1")

	g.RemoveTunnel("n1/t1")

	// Should be able to acquire again
	if ok, _ := g.AcquireConn("n1", "n1/t1"); !ok {
		t.Error("should succeed after RemoveTunnel")
	}
}

func TestGatewayLimiter_RemoveNode(t *testing.T) {
	cfg := GatewayRateLimitConfig{
		MaxConnsPerNode:   5,
		MaxConnsPerTunnel: 5,
		MaxBandwidthPerTunnel:   1024,
	}
	g := NewGatewayLimiter(cfg)

	_, _ = g.AcquireConn("n1", "n1/t1")
	_, _ = g.AcquireConn("n1", "n1/t2")
	_ = g.BWLimiterFor("n1/t1")
	_ = g.BWLimiterFor("n1/t2")

	g.RemoveNode("n1")

	// node-level counter should be reset
	for i := range 5 {
		sKey := "n1/t" + string(rune('0'+i))
		if ok, _ := g.AcquireConn("n1", sKey); !ok {
			t.Errorf("acquire %d after RemoveNode should succeed", i)
		}
	}
}

func TestGatewayLimiter_UpdateTunnelBPSNoDeadlock(t *testing.T) {
	cfg := GatewayRateLimitConfig{
		MaxBandwidthPerTunnel: 1024,
	}
	g := NewGatewayLimiter(cfg)

	// Create a bw limiter first
	_ = g.BWLimiterFor("n1/t1")

	// Update with BPS override — must not deadlock
	done := make(chan struct{})
	go func() {
		g.UpdateTunnelConfig("n1/t1", TunnelRateConfig{MaxBandwidth: 2048})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("UpdateTunnelConfig deadlocked")
	}
}

func TestGatewayLimiter_Concurrency(t *testing.T) {
	cfg := GatewayRateLimitConfig{
		MaxConnsPerNode:   100,
		MaxConnsPerTunnel: 100,
	}
	g := NewGatewayLimiter(cfg)

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sKey := "n1/t" + string(rune(idx))
			_, gen := g.AcquireConn("n1", sKey)
			g.ReleaseConn("n1", sKey, gen)
		}(i)
	}
	wg.Wait()
}

func TestGatewayLimiter_GenerationIsolation(t *testing.T) {
	cfg := GatewayRateLimitConfig{
		MaxConnsPerTunnel: 5,
	}
	g := NewGatewayLimiter(cfg)

	// Acquire a connection (gen=0)
	_, gen0 := g.AcquireConn("n1", "n1/t1")

	// RemoveTunnel bumps generation to 1
	g.RemoveTunnel("n1/t1")

	// New acquire gets gen=1
	ok, gen1 := g.AcquireConn("n1", "n1/t1")
	if !ok {
		t.Error("should succeed after RemoveTunnel")
	}
	if gen1 == gen0 {
		t.Error("generation should change after RemoveTunnel")
	}

	// Old release (gen=0) should be a no-op — must not decrement new count
	g.ReleaseConn("n1", "n1/t1", gen0)

	// New connection should still be tracked (count=1, not 0)
	if ok, _ := g.AcquireConn("n1", "n1/t1"); !ok {
		t.Error("old generation release should not decrement new count")
	}
}
