package node

import (
	"context"
	"testing"
	"time"

	"moleAgent_Serv/internal/core"
)

func TestCleanupDeadNodes_DoesNotRemovePreconfiguredOfflineNode(t *testing.T) {
	ctx := context.Background()
	mgr := NewShardedNodeManager(4)

	if err := mgr.Add(ctx, &core.Node{
		ID:     "NodeOff1",
		Name:   "NodeOff1",
		Status: core.NodeStatusOffline,
		Tunnels: []core.Tunnel{
			{Name: "web", Type: core.TunnelTypeHTTP, Target: "127.0.0.1:8080"},
		},
	}); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	callbackCalled := false
	cleanupDeadNodes(ctx, mgr, func(nodeID string, tunnels []core.Tunnel) {
		callbackCalled = true
	})

	if _, ok := mgr.Get(ctx, "NodeOff1"); !ok {
		t.Fatal("preconfigured offline node should not be removed by health check")
	}
	if callbackCalled {
		t.Fatal("disconnect callback should not be called for preconfigured offline node")
	}
}

func TestCleanupDeadNodes_RemovesHeartbeatTimedOutOnlineNode(t *testing.T) {
	ctx := context.Background()
	mgr := NewShardedNodeManager(4)
	lastHeartbeat := time.Now().Add(-heartbeatTimeout - time.Second)

	if err := mgr.Add(ctx, &core.Node{
		ID:            "NodeOn01",
		Name:          "NodeOn01",
		Status:        core.NodeStatusOnline,
		LastHeartbeat: &lastHeartbeat,
		Tunnels: []core.Tunnel{
			{Name: "ssh", Type: core.TunnelTypeTCP, Target: "127.0.0.1:22"},
		},
	}); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	callbackCount := 0
	var gotNodeID string
	var gotTunnels []core.Tunnel
	cleanupDeadNodes(ctx, mgr, func(nodeID string, tunnels []core.Tunnel) {
		callbackCount++
		gotNodeID = nodeID
		gotTunnels = tunnels
	})

	if _, ok := mgr.Get(ctx, "NodeOn01"); ok {
		t.Fatal("timed out online node should be removed by health check")
	}
	if callbackCount != 1 {
		t.Fatalf("expected disconnect callback once, got %d", callbackCount)
	}
	if gotNodeID != "NodeOn01" {
		t.Fatalf("expected callback node id NodeOn01, got %s", gotNodeID)
	}
	if len(gotTunnels) != 1 || gotTunnels[0].Name != "ssh" {
		t.Fatalf("expected tunnel snapshot in callback, got %+v", gotTunnels)
	}
}
