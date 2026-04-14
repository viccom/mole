package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/node"
)

type testNodeRepo struct {
	nodes       map[string]*core.Node
	updateCalls int
}

type testTunnelConfigManager struct {
	nodeMgr      core.NodeManager
	nodeRepo     core.NodeRepo
	replaceCalls int
	replacedTuns []core.Tunnel
}

func newTestNodeRepo() *testNodeRepo {
	return &testNodeRepo{nodes: make(map[string]*core.Node)}
}

func cloneTestNode(n *core.Node) *core.Node {
	c := *n
	c.Tunnels = append([]core.Tunnel(nil), n.Tunnels...)
	return &c
}

func (r *testNodeRepo) Create(node *core.Node) error {
	r.nodes[node.ID] = cloneTestNode(node)
	return nil
}

func (r *testNodeRepo) GetByID(id string) (*core.Node, error) {
	n, ok := r.nodes[id]
	if !ok {
		return nil, core.ErrNodeNotFound
	}
	return cloneTestNode(n), nil
}

func (r *testNodeRepo) GetAll() ([]*core.Node, error) {
	var out []*core.Node
	for _, n := range r.nodes {
		out = append(out, cloneTestNode(n))
	}
	return out, nil
}

func (r *testNodeRepo) Update(node *core.Node) error {
	r.updateCalls++
	r.nodes[node.ID] = cloneTestNode(node)
	return nil
}

func (r *testNodeRepo) Delete(id string) error {
	delete(r.nodes, id)
	return nil
}

func (m *testTunnelConfigManager) ApplyTunnel(context.Context, string, core.Tunnel) (core.TunnelChangeResult, error) {
	return core.TunnelChangeResult{}, nil
}

func (m *testTunnelConfigManager) RemoveTunnel(context.Context, string, string) (core.TunnelChangeResult, error) {
	return core.TunnelChangeResult{}, nil
}

func (m *testTunnelConfigManager) ReplaceTunnels(ctx context.Context, nodeID string, tunnels []core.Tunnel) (core.TunnelChangeResult, error) {
	m.replaceCalls++
	m.replacedTuns = append([]core.Tunnel(nil), tunnels...)
	if err := m.nodeMgr.Update(ctx, nodeID, func(n *core.Node) {
		n.Tunnels = append([]core.Tunnel(nil), tunnels...)
	}); err != nil {
		return core.TunnelChangeResult{}, err
	}
	if node, ok := m.nodeMgr.Get(ctx, nodeID); ok && m.nodeRepo != nil {
		if err := m.nodeRepo.Update(node); err != nil {
			return core.TunnelChangeResult{}, err
		}
	}
	return core.TunnelChangeResult{Status: "ok", Persisted: true, ClientSynced: true}, nil
}

func (m *testTunnelConfigManager) SyncFromClient(context.Context, string, []core.Tunnel) error {
	return nil
}

func (m *testTunnelConfigManager) LoadPersisted(context.Context, string) ([]core.Tunnel, error) {
	return nil, nil
}

func TestNodeHandlerUpdate_SuccessPersistsChanges(t *testing.T) {
	ctx := context.Background()
	nodeMgr := node.NewShardedNodeManager(4)
	nodeRepo := newTestNodeRepo()
	tunnelSvc := &testTunnelConfigManager{nodeMgr: nodeMgr, nodeRepo: nodeRepo}
	handler := NewNodeHandler(nodeMgr, nodeRepo, tunnelSvc)

	original := &core.Node{
		ID:     "Node0001",
		Name:   "old-name",
		Status: core.NodeStatusOnline,
		Tunnels: []core.Tunnel{
			{Name: "old", Type: core.TunnelTypeHTTP, Target: "http://127.0.0.1:8080"},
		},
	}
	if err := nodeMgr.Add(ctx, original); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if err := nodeRepo.Create(original); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	body, _ := json.Marshal(map[string]any{
		"name": "new-name",
		"tunnels": []core.Tunnel{
			{Name: "api", Type: core.TunnelTypeHTTP, Target: "http://127.0.0.1:9090"},
		},
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/nodes/Node0001", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler.Update(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}
	if nodeRepo.updateCalls != 1 {
		t.Fatalf("expected exactly one repo update, got %d", nodeRepo.updateCalls)
	}
	if tunnelSvc.replaceCalls != 1 {
		t.Fatalf("expected tunnel service replace to be called once, got %d", tunnelSvc.replaceCalls)
	}

	updated, ok := nodeMgr.Get(ctx, "Node0001")
	if !ok {
		t.Fatal("updated node not found in node manager")
	}
	if updated.Name != "new-name" {
		t.Fatalf("expected name updated, got %s", updated.Name)
	}
	if len(updated.Tunnels) != 1 || updated.Tunnels[0].Name != "api" {
		t.Fatalf("unexpected tunnels in node manager: %+v", updated.Tunnels)
	}

	persisted, err := nodeRepo.GetByID("Node0001")
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if persisted.Name != "new-name" {
		t.Fatalf("expected persisted name updated, got %s", persisted.Name)
	}
	if len(persisted.Tunnels) != 1 || persisted.Tunnels[0].Name != "api" {
		t.Fatalf("unexpected persisted tunnels: %+v", persisted.Tunnels)
	}
}

func TestNodeHandlerUpdate_NodeNotFound(t *testing.T) {
	nodeMgr := node.NewShardedNodeManager(4)
	nodeRepo := newTestNodeRepo()
	handler := NewNodeHandler(nodeMgr, nodeRepo, &testTunnelConfigManager{nodeMgr: nodeMgr, nodeRepo: nodeRepo})

	body := []byte(`{"name":"missing"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/nodes/Node404", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler.Update(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d, body=%s", w.Code, w.Body.String())
	}
	if nodeRepo.updateCalls != 0 {
		t.Fatalf("repo update should not be called when node is missing")
	}
}
