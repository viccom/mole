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

func (m *testTunnelConfigManager) MoveTunnel(_ context.Context, _, _ string, _ core.Tunnel) (core.TunnelChangeResult, error) {
	return core.TunnelChangeResult{}, nil
}

func (m *testTunnelConfigManager) RemoveTunnel(ctx context.Context, nodeID string, tunnelName string) (core.TunnelChangeResult, error) {
	if err := m.nodeMgr.Update(ctx, nodeID, func(n *core.Node) {
		updated := make([]core.Tunnel, 0, len(n.Tunnels))
		for _, t := range n.Tunnels {
			if t.Name != tunnelName {
				updated = append(updated, t)
			}
		}
		n.Tunnels = updated
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

func (m *testTunnelConfigManager) UpdateNodeRateLimit(context.Context, string, *core.NodeRateLimit) error {
	return nil
}

func (m *testTunnelConfigManager) BatchUpdateRateLimit(context.Context, []core.RateLimitItem) ([]core.TunnelChangeResult, error) {
	return nil, nil
}

func TestNodeHandlerUpdate_SuccessPersistsChanges(t *testing.T) {
	ctx := context.Background()
	nodeMgr := node.NewShardedNodeManager(4)
	nodeRepo := newTestNodeRepo()
	tunnelSvc := &testTunnelConfigManager{nodeMgr: nodeMgr, nodeRepo: nodeRepo}
	handler := NewNodeHandler(nodeMgr, nodeRepo, tunnelSvc, nil)

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
	handler := NewNodeHandler(nodeMgr, nodeRepo, &testTunnelConfigManager{nodeMgr: nodeMgr, nodeRepo: nodeRepo}, nil)

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

func TestNodeHandler_List_NonAdminFiltersByOwner(t *testing.T) {
	ctx := context.Background()
	nodeMgr := node.NewShardedNodeManager(4)
	nodeRepo := newTestNodeRepo()
	handler := NewNodeHandler(nodeMgr, nodeRepo, &testTunnelConfigManager{nodeMgr: nodeMgr, nodeRepo: nodeRepo}, nil)

	// Create 3 nodes: 2 owned by userA, 1 owned by userB
	nodes := []*core.Node{
		{ID: "NodeA1", Name: "a1", Status: core.NodeStatusOnline, OwnerUserID: "userA"},
		{ID: "NodeA2", Name: "a2", Status: core.NodeStatusOnline, OwnerUserID: "userA"},
		{ID: "NodeB1", Name: "b1", Status: core.NodeStatusOnline, OwnerUserID: "userB"},
	}
	for _, n := range nodes {
		if err := nodeMgr.Add(ctx, n); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}

	// Inject userA claims (non-admin)
	claims := &core.Claims{UserID: "userA", Roles: []string{"operator"}}
	req := reqWithClaims(http.MethodGet, "/api/v1/nodes", nil, claims)
	w := httptest.NewRecorder()

	handler.List(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}

	resp := parseResponse(t, w)
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected data type: %T", resp.Data)
	}
	items, _ := data["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("expected 2 items for userA, got %d", len(items))
	}
	total, _ := data["total"].(float64)
	if total != 2 {
		t.Fatalf("expected total=2, got %v", total)
	}
}

func TestNodeHandler_List_AdminSeesAll(t *testing.T) {
	ctx := context.Background()
	nodeMgr := node.NewShardedNodeManager(4)
	nodeRepo := newTestNodeRepo()
	handler := NewNodeHandler(nodeMgr, nodeRepo, &testTunnelConfigManager{nodeMgr: nodeMgr, nodeRepo: nodeRepo}, nil)

	nodes := []*core.Node{
		{ID: "NodeA1", Name: "a1", Status: core.NodeStatusOnline, OwnerUserID: "userA"},
		{ID: "NodeA2", Name: "a2", Status: core.NodeStatusOnline, OwnerUserID: "userA"},
		{ID: "NodeB1", Name: "b1", Status: core.NodeStatusOnline, OwnerUserID: "userB"},
	}
	for _, n := range nodes {
		if err := nodeMgr.Add(ctx, n); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}

	// Inject admin claims
	claims := &core.Claims{UserID: "admin", Roles: []string{"admin"}}
	req := reqWithClaims(http.MethodGet, "/api/v1/nodes", nil, claims)
	w := httptest.NewRecorder()

	handler.List(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}

	resp := parseResponse(t, w)
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected data type: %T", resp.Data)
	}
	items, _ := data["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("admin should see all 3 items, got %d", len(items))
	}
	total, _ := data["total"].(float64)
	if total != 3 {
		t.Fatalf("expected total=3, got %v", total)
	}
}

func TestNodeHandler_Get_NonOwnerGets404(t *testing.T) {
	ctx := context.Background()
	nodeMgr := node.NewShardedNodeManager(4)
	nodeRepo := newTestNodeRepo()
	handler := NewNodeHandler(nodeMgr, nodeRepo, &testTunnelConfigManager{nodeMgr: nodeMgr, nodeRepo: nodeRepo}, nil)

	n := &core.Node{ID: "NodeB1", Name: "b1", Status: core.NodeStatusOnline, OwnerUserID: "userB"}
	if err := nodeMgr.Add(ctx, n); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Inject userA claims (non-admin, non-owner)
	claims := &core.Claims{UserID: "userA", Roles: []string{"operator"}}
	req := reqWithClaims(http.MethodGet, "/api/v1/nodes/NodeB1", nil, claims)
	w := httptest.NewRecorder()

	handler.Get(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-owner, got %d, body=%s", w.Code, w.Body.String())
	}
}

func TestNodeHandler_Create_BindsOwnerUserID(t *testing.T) {
	ctx := context.Background()
	nodeMgr := node.NewShardedNodeManager(4)
	nodeRepo := newTestNodeRepo()
	handler := NewNodeHandler(nodeMgr, nodeRepo, &testTunnelConfigManager{nodeMgr: nodeMgr, nodeRepo: nodeRepo}, nil)

	// Step 2: Inject non-admin userA claims
	claims := &core.Claims{UserID: "userA", Roles: []string{"operator"}}
	body, _ := json.Marshal(map[string]any{
		"name": "test-node-1",
	})
	req := reqWithClaims(http.MethodPost, "/api/v1/nodes", body, claims)
	w := httptest.NewRecorder()

	// Step 3: POST to create a node
	handler.Create(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}

	// Step 4: Verify the created node has OwnerUserID="userA"
	gotNode, ok := nodeMgr.Get(ctx, "test-node-1")
	if !ok {
		t.Fatal("created node not found in nodeMgr")
	}
	if gotNode.OwnerUserID != "userA" {
		t.Fatalf("expected OwnerUserID=userA, got %s", gotNode.OwnerUserID)
	}

	// Also verify persisted node has correct owner
	persisted, err := nodeRepo.GetByID("test-node-1")
	if err != nil {
		t.Fatalf("persisted node not found: %v", err)
	}
	if persisted.OwnerUserID != "userA" {
		t.Fatalf("expected persisted OwnerUserID=userA, got %s", persisted.OwnerUserID)
	}
}

func TestNodeHandler_Delete_NonOwnerGets404(t *testing.T) {
	ctx := context.Background()
	nodeMgr := node.NewShardedNodeManager(4)
	nodeRepo := newTestNodeRepo()
	handler := NewNodeHandler(nodeMgr, nodeRepo, &testTunnelConfigManager{nodeMgr: nodeMgr, nodeRepo: nodeRepo}, nil)

	n := &core.Node{ID: "NodeB1", Name: "b1", Status: core.NodeStatusOnline, OwnerUserID: "userB"}
	if err := nodeMgr.Add(ctx, n); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Inject userA claims (non-admin, non-owner)
	claims := &core.Claims{UserID: "userA", Roles: []string{"operator"}}
	req := reqWithClaims(http.MethodDelete, "/api/v1/nodes/NodeB1", nil, claims)
	w := httptest.NewRecorder()

	handler.Delete(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-owner delete, got %d, body=%s", w.Code, w.Body.String())
	}

	// Node should still exist
	if _, ok := nodeMgr.Get(ctx, "NodeB1"); !ok {
		t.Fatal("node should still exist after non-owner delete attempt")
	}
}

// ===== 审查 #5：节点删除级联吊销 p2p 信令凭据 =====

type fakeP2PRevoker struct {
	calls   []string
	tunnels []core.Tunnel
}

func (f *fakeP2PRevoker) RevokeNodeP2PTokens(nodeID string, tunnels []core.Tunnel) {
	f.calls = append(f.calls, nodeID)
	f.tunnels = append([]core.Tunnel(nil), tunnels...)
}

// 删除节点必须把其隧道列表交给凭据吊销器——否则被删节点的信令凭据
// 存活到 TTL，恰好赶上被释放的 room 被新配对注册（审查 #5 场景）
func TestNodeHandlerDelete_RevokesP2PTokens(t *testing.T) {
	ctx := context.Background()
	nodeMgr := node.NewShardedNodeManager(4)
	if err := nodeMgr.Add(ctx, &core.Node{
		ID:     "Node0001",
		Name:   "n1",
		Status: core.NodeStatusOnline,
		Tunnels: []core.Tunnel{
			{Name: "p2p-a", Type: core.TunnelTypeP2P},
			{Name: "web", Type: core.TunnelTypeHTTP, Target: "127.0.0.1:80"},
		},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	repo := newTestNodeRepo()
	handler := NewNodeHandler(nodeMgr, repo, &testTunnelConfigManager{}, nil)
	revoker := &fakeP2PRevoker{}
	handler.SetP2PTokenRevoker(revoker)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/nodes/Node0001", nil)
	rec := httptest.NewRecorder()
	handler.Delete(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d", rec.Code)
	}

	if len(revoker.calls) != 1 || revoker.calls[0] != "Node0001" {
		t.Fatalf("revoker calls = %v, want [Node0001]", revoker.calls)
	}
	found := false
	for _, tl := range revoker.tunnels {
		if tl.Name == "p2p-a" && tl.Type == core.TunnelTypeP2P {
			found = true
		}
	}
	if !found {
		t.Fatalf("revoker must receive the node's p2p tunnels, got %+v", revoker.tunnels)
	}
}
