package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/node"
)

func TestTunnelHandler_List_NonAdminFiltersByOwner(t *testing.T) {
	ctx := context.Background()
	nodeMgr := node.NewShardedNodeManager(4)
	tunnelSvc := &testTunnelConfigManager{nodeMgr: nodeMgr}
	handler := NewTunnelHandler(nodeMgr, tunnelSvc, nil)

	// userA: 2 online nodes with tunnels
	nodeA1 := &core.Node{
		ID: "NodeA1", Name: "a1", Status: core.NodeStatusOnline, OwnerUserID: "userA",
		Tunnels: []core.Tunnel{
			{Name: "web", Type: core.TunnelTypeHTTP, Target: "http://127.0.0.1:8080"},
			{Name: "api", Type: core.TunnelTypeHTTP, Target: "http://127.0.0.1:9090"},
		},
	}
	nodeA2 := &core.Node{
		ID: "NodeA2", Name: "a2", Status: core.NodeStatusOnline, OwnerUserID: "userA",
		Tunnels: []core.Tunnel{
			{Name: "ssh", Type: core.TunnelTypeTCP, Target: "127.0.0.1:22"},
		},
	}
	// userB: 1 online node with 1 tunnel
	nodeB1 := &core.Node{
		ID: "NodeB1", Name: "b1", Status: core.NodeStatusOnline, OwnerUserID: "userB",
		Tunnels: []core.Tunnel{
			{Name: "db", Type: core.TunnelTypeTCP, Target: "127.0.0.1:3306"},
		},
	}
	for _, n := range []*core.Node{nodeA1, nodeA2, nodeB1} {
		if err := nodeMgr.Add(ctx, n); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}

	// Inject userA claims (non-admin)
	claims := &core.Claims{UserID: "userA", Roles: []string{"operator"}}
	req := reqWithClaims(http.MethodGet, "/api/v1/tunnels", nil, claims)
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
		t.Fatalf("expected 3 tunnels for userA (2+1), got %d", len(items))
	}
	total, _ := data["total"].(float64)
	if total != 3 {
		t.Fatalf("expected total=3, got %v", total)
	}
}

func TestTunnelHandler_List_DisabledTunnelShown(t *testing.T) {
	ctx := context.Background()
	nodeMgr := node.NewShardedNodeManager(4)
	tunnelSvc := &testTunnelConfigManager{nodeMgr: nodeMgr}
	handler := NewTunnelHandler(nodeMgr, tunnelSvc, nil)

	disabled := false
	enabled := true

	n := &core.Node{
		ID: "Node1", Name: "n1", Status: core.NodeStatusOnline, OwnerUserID: "admin",
		Tunnels: []core.Tunnel{
			{Name: "active-web", Type: core.TunnelTypeHTTP, Target: "http://127.0.0.1:8080", Enabled: &enabled},
			{Name: "disabled-web", Type: core.TunnelTypeHTTP, Target: "http://127.0.0.1:8081", Enabled: &disabled},
		},
	}
	if err := nodeMgr.Add(ctx, n); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Admin claims
	claims := &core.Claims{UserID: "admin", Roles: []string{"admin"}}
	req := reqWithClaims(http.MethodGet, "/api/v1/tunnels", nil, claims)
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
		t.Fatalf("expected 2 tunnels (both shown), got %d", len(items))
	}

	// Verify one has enabled=false by re-encoding items
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			if enabled, _ := m["enabled"].(bool); !enabled {
				// Found a disabled tunnel
				return
			}
		}
	}
	t.Fatal("response should contain a tunnel with enabled=false")
}

func TestTunnelHandler_Stats_EnabledAndActive(t *testing.T) {
	ctx := context.Background()
	nodeMgr := node.NewShardedNodeManager(4)
	tunnelSvc := &testTunnelConfigManager{nodeMgr: nodeMgr}
	handler := NewTunnelHandler(nodeMgr, tunnelSvc, nil)

	disabled := false

	// Online node: 3 tunnels (2 enabled, 1 disabled)
	onlineNode := &core.Node{
		ID: "NodeOnline", Name: "online", Status: core.NodeStatusOnline,
		Tunnels: []core.Tunnel{
			{Name: "t1", Type: core.TunnelTypeHTTP, Target: "http://127.0.0.1:8080"},
			{Name: "t2", Type: core.TunnelTypeTCP, Target: "127.0.0.1:22"},
			{Name: "t3", Type: core.TunnelTypeUDP, Target: "127.0.0.1:53", Enabled: &disabled},
		},
	}
	// Offline node: 2 tunnels (both enabled)
	offlineNode := &core.Node{
		ID: "NodeOffline", Name: "offline", Status: core.NodeStatusOffline,
		Tunnels: []core.Tunnel{
			{Name: "t4", Type: core.TunnelTypeHTTP, Target: "http://127.0.0.1:9090"},
			{Name: "t5", Type: core.TunnelTypeTCP, Target: "127.0.0.1:3306"},
		},
	}
	for _, n := range []*core.Node{onlineNode, offlineNode} {
		if err := nodeMgr.Add(ctx, n); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tunnels/stats", nil)
	w := httptest.NewRecorder()

	handler.Stats(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}

	resp := parseResponse(t, w)
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected data type: %T", resp.Data)
	}

	// total_tunnels=5
	if v, _ := data["total_tunnels"].(float64); v != 5 {
		t.Fatalf("expected total_tunnels=5, got %v", v)
	}
	// enabled_tunnels=4 (t1, t2, t4, t5 — t3 is disabled)
	if v, _ := data["enabled_tunnels"].(float64); v != 4 {
		t.Fatalf("expected enabled_tunnels=4, got %v", v)
	}
	// active_tunnels=2 (only online+enabled: t1 and t2)
	if v, _ := data["active_tunnels"].(float64); v != 2 {
		t.Fatalf("expected active_tunnels=2, got %v", v)
	}
}

func TestTunnelHandler_Delete_NonOwner_404(t *testing.T) {
	ctx := context.Background()
	nodeMgr := node.NewShardedNodeManager(4)
	tunnelSvc := &testTunnelConfigManager{nodeMgr: nodeMgr}
	handler := NewTunnelHandler(nodeMgr, tunnelSvc, nil)

	// Node owned by userB with a tunnel
	n := &core.Node{
		ID: "NodeB1", Name: "b1", Status: core.NodeStatusOnline, OwnerUserID: "userB",
		Tunnels: []core.Tunnel{
			{Name: "secret-db", Type: core.TunnelTypeTCP, Target: "127.0.0.1:3306"},
		},
	}
	if err := nodeMgr.Add(ctx, n); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Inject userA claims (non-admin, non-owner)
	claims := &core.Claims{UserID: "userA", Roles: []string{"operator"}}
	req := reqWithClaims(http.MethodDelete, "/api/v1/tunnels/secret-db", nil, claims)
	w := httptest.NewRecorder()

	handler.Delete(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-owner tunnel delete, got %d, body=%s", w.Code, w.Body.String())
	}

	// Tunnel should still exist
	gotNode, ok := nodeMgr.Get(ctx, "NodeB1")
	if !ok {
		t.Fatal("node should still exist")
	}
	if len(gotNode.Tunnels) != 1 {
		t.Fatalf("tunnel should still exist, got %d tunnels", len(gotNode.Tunnels))
	}
}

func TestTunnelHandler_List_AdminSeesAll(t *testing.T) {
	ctx := context.Background()
	nodeMgr := node.NewShardedNodeManager(4)
	tunnelSvc := &testTunnelConfigManager{nodeMgr: nodeMgr}
	handler := NewTunnelHandler(nodeMgr, tunnelSvc, nil)

	nodeA := &core.Node{
		ID: "NodeA1", Name: "a1", Status: core.NodeStatusOnline, OwnerUserID: "userA",
		Tunnels: []core.Tunnel{
			{Name: "web", Type: core.TunnelTypeHTTP, Target: "http://127.0.0.1:8080"},
		},
	}
	nodeB := &core.Node{
		ID: "NodeB1", Name: "b1", Status: core.NodeStatusOnline, OwnerUserID: "userB",
		Tunnels: []core.Tunnel{
			{Name: "db", Type: core.TunnelTypeTCP, Target: "127.0.0.1:3306"},
		},
	}
	for _, n := range []*core.Node{nodeA, nodeB} {
		if err := nodeMgr.Add(ctx, n); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}

	// Admin claims
	claims := &core.Claims{UserID: "admin", Roles: []string{"admin"}}
	req := reqWithClaims(http.MethodGet, "/api/v1/tunnels", nil, claims)
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
		t.Fatalf("admin should see all 2 tunnels, got %d", len(items))
	}
	total, _ := data["total"].(float64)
	if total != 2 {
		t.Fatalf("expected total=2, got %v", total)
	}
}

func TestTunnelHandler_Create_NonOwner_404(t *testing.T) {
	ctx := context.Background()
	nodeMgr := node.NewShardedNodeManager(4)
	tunnelSvc := &testTunnelConfigManager{nodeMgr: nodeMgr}
	handler := NewTunnelHandler(nodeMgr, tunnelSvc, nil)

	// Step 1: Create a node owned by userB
	n := &core.Node{
		ID: "NodeB1", Name: "b1", Status: core.NodeStatusOnline, OwnerUserID: "userB",
		Tunnels: []core.Tunnel{},
	}
	if err := nodeMgr.Add(ctx, n); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Step 2: Inject userA claims (non-admin, non-owner)
	claims := &core.Claims{UserID: "userA", Roles: []string{"operator"}}
	body, _ := json.Marshal(map[string]any{
		"name":    "evil-tunnel",
		"type":    "http",
		"target":  "http://127.0.0.1:6666",
		"node_id": "NodeB1",
	})
	req := reqWithClaims(http.MethodPost, "/api/v1/tunnels", body, claims)
	w := httptest.NewRecorder()

	// Step 3 & 4: POST tunnel for userB's node -- should get 404
	handler.Create(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-owner tunnel create, got %d, body=%s", w.Code, w.Body.String())
	}

	// Step 5: Verify tunnel was NOT added to the node
	gotNode, ok := nodeMgr.Get(ctx, "NodeB1")
	if !ok {
		t.Fatal("node should still exist")
	}
	if len(gotNode.Tunnels) != 0 {
		t.Fatalf("no tunnel should be added, got %d tunnels", len(gotNode.Tunnels))
	}
}

func TestTunnelHandler_Stats_NonAdminOnlyOwnTunnels(t *testing.T) {
	ctx := context.Background()
	nodeMgr := node.NewShardedNodeManager(4)
	tunnelSvc := &testTunnelConfigManager{nodeMgr: nodeMgr}
	handler := NewTunnelHandler(nodeMgr, tunnelSvc, nil)

	// Step 1: userA has 3 tunnels across 2 nodes
	nodeA1 := &core.Node{
		ID: "NodeA1", Name: "a1", Status: core.NodeStatusOnline, OwnerUserID: "userA",
		Tunnels: []core.Tunnel{
			{Name: "web", Type: core.TunnelTypeHTTP, Target: "http://127.0.0.1:8080"},
			{Name: "api", Type: core.TunnelTypeHTTP, Target: "http://127.0.0.1:9090"},
		},
	}
	nodeA2 := &core.Node{
		ID: "NodeA2", Name: "a2", Status: core.NodeStatusOnline, OwnerUserID: "userA",
		Tunnels: []core.Tunnel{
			{Name: "ssh", Type: core.TunnelTypeTCP, Target: "127.0.0.1:22"},
		},
	}
	// userB has 2 tunnels
	nodeB1 := &core.Node{
		ID: "NodeB1", Name: "b1", Status: core.NodeStatusOnline, OwnerUserID: "userB",
		Tunnels: []core.Tunnel{
			{Name: "db", Type: core.TunnelTypeTCP, Target: "127.0.0.1:3306"},
			{Name: "cache", Type: core.TunnelTypeTCP, Target: "127.0.0.1:6379"},
		},
	}
	for _, n := range []*core.Node{nodeA1, nodeA2, nodeB1} {
		if err := nodeMgr.Add(ctx, n); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}

	// Step 2: Inject userA claims (non-admin)
	claims := &core.Claims{UserID: "userA", Roles: []string{"operator"}}
	req := reqWithClaims(http.MethodGet, "/api/v1/tunnels/stats", nil, claims)
	w := httptest.NewRecorder()

	// Step 3: Call Stats
	handler.Stats(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}

	// Step 4: Verify total_tunnels=3 (only userA's tunnels)
	resp := parseResponse(t, w)
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected data type: %T", resp.Data)
	}
	if v, _ := data["total_tunnels"].(float64); v != 3 {
		t.Fatalf("expected total_tunnels=3 (only userA's), got %v", v)
	}
	if v, _ := data["active_tunnels"].(float64); v != 3 {
		t.Fatalf("expected active_tunnels=3 (only userA's, all online+enabled), got %v", v)
	}
}

type mockTunnelStatsReader struct {
	all map[string]*core.TunnelRuntimeStats
}

func (m *mockTunnelStatsReader) Get(name string) *core.TunnelRuntimeStats {
	if m == nil {
		return nil
	}
	return m.all[name]
}

func (m *mockTunnelStatsReader) GetAll() map[string]*core.TunnelRuntimeStats {
	if m == nil {
		return nil
	}
	return m.all
}

func (m *mockTunnelStatsReader) Remove(name string) {
	delete(m.all, name)
}

func TestTunnelHandler_Usage_UsesCompositeStatsKey(t *testing.T) {
	ctx := context.Background()
	nodeMgr := node.NewShardedNodeManager(4)
	tunnelSvc := &testTunnelConfigManager{nodeMgr: nodeMgr}
	stats := &mockTunnelStatsReader{
		all: map[string]*core.TunnelRuntimeStats{
			"NodeA1/shared": {BytesIn: 100, BytesOut: 200, TotalConns: 3, ActiveConns: 1, LastActivity: "2026-04-16T10:00:00Z"},
			"NodeB1/shared": {BytesIn: 300, BytesOut: 400, TotalConns: 5, ActiveConns: 2, LastActivity: "2026-04-16T11:00:00Z"},
		},
	}
	handler := NewTunnelHandler(nodeMgr, tunnelSvc, stats)

	nodeA := &core.Node{
		ID: "NodeA1", Name: "a1", Status: core.NodeStatusOnline, OwnerUserID: "userA",
		Tunnels: []core.Tunnel{
			{Name: "shared", Type: core.TunnelTypeTCP, Target: "127.0.0.1:22"},
		},
	}
	nodeB := &core.Node{
		ID: "NodeB1", Name: "b1", Status: core.NodeStatusOnline, OwnerUserID: "userB",
		Tunnels: []core.Tunnel{
			{Name: "shared", Type: core.TunnelTypeTCP, Target: "127.0.0.1:2222"},
		},
	}
	for _, n := range []*core.Node{nodeA, nodeB} {
		if err := nodeMgr.Add(ctx, n); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tunnels/usage", nil)
	w := httptest.NewRecorder()
	handler.Usage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}

	resp := parseResponse(t, w)
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected data type: %T", resp.Data)
	}
	items, ok := data["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("expected 2 usage items, got %#v", data["items"])
	}

	gotByNode := make(map[string]map[string]any, 2)
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("unexpected item type: %T", item)
		}
		nodeID, _ := m["node_id"].(string)
		gotByNode[nodeID] = m
	}

	if v, _ := gotByNode["NodeA1"]["bytes_in"].(float64); v != 100 {
		t.Fatalf("expected NodeA1 bytes_in=100, got %v", v)
	}
	if v, _ := gotByNode["NodeB1"]["bytes_in"].(float64); v != 300 {
		t.Fatalf("expected NodeB1 bytes_in=300, got %v", v)
	}
	if v, _ := gotByNode["NodeA1"]["active_connections"].(float64); v != 1 {
		t.Fatalf("expected NodeA1 active_connections=1, got %v", v)
	}
	if v, _ := gotByNode["NodeB1"]["active_connections"].(float64); v != 2 {
		t.Fatalf("expected NodeB1 active_connections=2, got %v", v)
	}
}

// containsString checks if substr appears in s.
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
