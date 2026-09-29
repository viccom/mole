package node

import (
	"context"
	"fmt"
	"testing"

	"moleAgent_Serv/internal/core"
)

func TestShardedNodeManager(t *testing.T) {
	mgr := NewShardedNodeManager(16)
	ctx := context.Background()

	// Add
	node1 := &core.Node{ID: "node-001", Name: "Test Node", Status: core.NodeStatusOnline}
	if err := mgr.Add(ctx, node1); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Get
	got, ok := mgr.Get(ctx, "node-001")
	if !ok {
		t.Fatal("Get returned false")
	}
	if got.ID != "node-001" || got.Name != "Test Node" {
		t.Errorf("Get returned wrong node: %+v", got)
	}

	// Get non-existent
	_, ok = mgr.Get(ctx, "nonexistent")
	if ok {
		t.Error("Get should return false for nonexistent node")
	}

	// GetAll
	all := mgr.GetAll(ctx)
	if len(all) != 1 {
		t.Errorf("expected 1 node, got %d", len(all))
	}

	// Remove
	if err := mgr.Remove(ctx, "node-001"); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	_, ok = mgr.Get(ctx, "node-001")
	if ok {
		t.Error("node should be removed")
	}
}

func TestShardedNodeManagerMultiple(t *testing.T) {
	mgr := NewShardedNodeManager(4)
	ctx := context.Background()

	for i := 0; i < 100; i++ {
		node := &core.Node{
			ID:     fmt.Sprintf("node-%d", i),
			Name:   fmt.Sprintf("Node %d", i),
			Status: core.NodeStatusOnline,
		}
		mgr.Add(ctx, node)
	}

	all := mgr.GetAll(ctx)
	if len(all) != 100 {
		t.Errorf("expected 100 nodes, got %d", len(all))
	}
}

func TestShardedNodeManagerDisconnect(t *testing.T) {
	mgr := NewShardedNodeManager(4)
	ctx := context.Background()

	node := &core.Node{ID: "n1", Status: core.NodeStatusOnline}
	mgr.Add(ctx, node)

	err := mgr.Disconnect(ctx, "n1")
	if err != nil {
		t.Fatalf("Disconnect failed: %v", err)
	}

	_, ok := mgr.Get(ctx, "n1")
	if ok {
		t.Error("node should be removed after disconnect")
	}
}

func TestShardedNodeManagerDisconnectNonexistent(t *testing.T) {
	mgr := NewShardedNodeManager(4)
	ctx := context.Background()

	err := mgr.Disconnect(ctx, "nonexistent")
	if err != core.ErrNodeNotFound {
		t.Errorf("expected ErrNodeNotFound, got %v", err)
	}
}

func TestNewShardedNodeManagerDefault(t *testing.T) {
	mgr := NewShardedNodeManager(0)
	if mgr == nil {
		t.Fatal("should create with default shard count")
	}
	if mgr.shardCount != defaultShardCount {
		t.Errorf("expected shard count %d, got %d", defaultShardCount, mgr.shardCount)
	}
}
