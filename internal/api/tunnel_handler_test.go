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

// Stats 分类型计数必须包含 p2p 专属键：p2p 计入 total_tunnels 却没有
// 专属计数键时，按类型键聚合的仪表盘会把 p2p 部署显示为空（审查 #15）
func TestTunnelHandlerStats_IncludesP2P(t *testing.T) {
	nodeMgr := node.NewShardedNodeManager(4)
	if err := nodeMgr.Add(context.Background(), &core.Node{
		ID:     "Node0001",
		Name:   "n1",
		Status: core.NodeStatusOnline,
		Tunnels: []core.Tunnel{
			{Name: "p2p-a", Type: core.TunnelTypeP2P},
			{Name: "p2p-b", Type: core.TunnelTypeP2P, Enabled: new(bool)}, // 禁用也计数（配置存在）
			{Name: "web", Type: core.TunnelTypeHTTP, Target: "http://127.0.0.1:80"},
			{Name: "ser", Type: "ser2mq", Para: []byte(`{}`)},
		},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	handler := NewTunnelHandler(nodeMgr, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tunnels/stats", nil)
	rec := httptest.NewRecorder()
	handler.Stats(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stats status = %d, body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data["p2p_tunnels"].(float64) != 2 {
		t.Fatalf("p2p_tunnels = %v, want 2", resp.Data["p2p_tunnels"])
	}
	if resp.Data["total_tunnels"].(float64) != 4 {
		t.Fatalf("total_tunnels = %v, want 4", resp.Data["total_tunnels"])
	}
}
