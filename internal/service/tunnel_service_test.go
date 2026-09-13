package service

import (
	"context"
	"encoding/json"
	"testing"

	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/node"
	"moleAgent_Serv/internal/tunnel"
)

type mockNodeRepo struct {
	nodes map[string]*core.Node
}

func newMockNodeRepo() *mockNodeRepo {
	return &mockNodeRepo{nodes: make(map[string]*core.Node)}
}

func (r *mockNodeRepo) Create(n *core.Node) error {
	r.nodes[n.ID] = cloneNode(n)
	return nil
}

func (r *mockNodeRepo) GetByID(id string) (*core.Node, error) {
	n, ok := r.nodes[id]
	if !ok {
		return nil, core.ErrNodeNotFound
	}
	return cloneNode(n), nil
}

func (r *mockNodeRepo) GetAll() ([]*core.Node, error) {
	out := make([]*core.Node, 0, len(r.nodes))
	for _, n := range r.nodes {
		out = append(out, cloneNode(n))
	}
	return out, nil
}

func (r *mockNodeRepo) Update(n *core.Node) error {
	r.nodes[n.ID] = cloneNode(n)
	return nil
}

func (r *mockNodeRepo) Delete(id string) error {
	delete(r.nodes, id)
	return nil
}

type mockGateway struct {
	rebuilds int
	stopped  []string
	stats    *mockStatsReader
}

type mockStatsReader struct {
	removed []string
}

func (s *mockStatsReader) Get(_ string) *core.TunnelRuntimeStats { return nil }
func (s *mockStatsReader) GetAll() map[string]*core.TunnelRuntimeStats {
	return nil
}
func (s *mockStatsReader) Remove(name string) {
	s.removed = append(s.removed, name)
}

func (g *mockGateway) RebuildIndex(_ context.Context)                  { g.rebuilds++ }
func (g *mockGateway) StartTCP(_ context.Context, _ core.Tunnel) error { return nil }
func (g *mockGateway) StartUDP(_ context.Context, _ core.Tunnel) error { return nil }
func (g *mockGateway) StopTunnel(name string)                           { g.stopped = append(g.stopped, name) }
func (g *mockGateway) Registry() *tunnel.ListenerRegistry               { return nil }
func (g *mockGateway) Stats() core.TunnelStatsReader                    { return g.stats }

type mockPusher struct {
	err   error
	calls int
}

func (p *mockPusher) PushTunnelUpdate(_ context.Context, _ string, _ []core.Tunnel) error {
	p.calls++
	return p.err
}

func cloneNode(n *core.Node) *core.Node {
	copied := *n
	copied.Tunnels = append([]core.Tunnel(nil), n.Tunnels...)
	return &copied
}

func TestApplyTunnel_PushFailureDoesNotFlipRuntime(t *testing.T) {
	ctx := context.Background()
	nodeMgr := node.NewShardedNodeManager(4)
	repo := newMockNodeRepo()
	gateway := &mockGateway{}
	pusher := &mockPusher{err: core.ErrNodeOffline}
	svc := NewTunnelConfigService(nil, nodeMgr, repo, gateway, pusher, nil, nil)

	online := &core.Node{
		ID:     "Node0001",
		Name:   "Node0001",
		Status: core.NodeStatusOnline,
		Tunnels: []core.Tunnel{
			{Name: "old", Type: core.TunnelTypeHTTP, Target: "127.0.0.1:8080"},
		},
	}
	if err := nodeMgr.Add(ctx, online); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	newTunnel := core.Tunnel{Name: "new", Type: core.TunnelTypeHTTP, Target: "127.0.0.1:9090"}
	result, err := svc.ApplyTunnel(ctx, online.ID, newTunnel)
	if err != nil {
		t.Fatalf("ApplyTunnel returned unexpected error: %v", err)
	}
	if !result.Persisted {
		t.Fatal("expected persisted=true")
	}
	if result.ClientSynced {
		t.Fatal("expected client_synced=false on push failure")
	}
	if result.Status != "synced_server_only" {
		t.Fatalf("expected synced_server_only, got %s", result.Status)
	}
	if result.Warning == "" {
		t.Fatal("expected warning on push failure")
	}

	runtimeNode, ok := nodeMgr.Get(ctx, online.ID)
	if !ok {
		t.Fatal("runtime node missing")
	}
	if len(runtimeNode.Tunnels) != 1 || runtimeNode.Tunnels[0].Name != "old" {
		t.Fatalf("runtime tunnels should stay unchanged on push failure: %+v", runtimeNode.Tunnels)
	}
	if gateway.rebuilds != 0 {
		t.Fatalf("runtime index should not rebuild on push failure, got %d", gateway.rebuilds)
	}

	persisted, err := repo.GetByID(online.ID)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if len(persisted.Tunnels) != 2 || persisted.Tunnels[1].Name != "new" {
		t.Fatalf("persisted tunnels should contain new config: %+v", persisted.Tunnels)
	}
}

func TestApplyTunnel_SuccessUpdatesRuntimeAndPersistence(t *testing.T) {
	ctx := context.Background()
	nodeMgr := node.NewShardedNodeManager(4)
	repo := newMockNodeRepo()
	gateway := &mockGateway{}
	pusher := &mockPusher{}
	svc := NewTunnelConfigService(nil, nodeMgr, repo, gateway, pusher, nil, nil)

	n := &core.Node{ID: "Node0002", Name: "Node0002", Status: core.NodeStatusOnline}
	if err := nodeMgr.Add(ctx, n); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	tunnelCfg := core.Tunnel{Name: "api", Type: core.TunnelTypeHTTP, Target: "127.0.0.1:8081"}
	result, err := svc.ApplyTunnel(ctx, n.ID, tunnelCfg)
	if err != nil {
		t.Fatalf("ApplyTunnel failed: %v", err)
	}
	if !result.Persisted || !result.ClientSynced || result.Status != "ok" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if pusher.calls != 1 {
		t.Fatalf("expected one push call, got %d", pusher.calls)
	}
	if gateway.rebuilds != 1 {
		t.Fatalf("expected one rebuild, got %d", gateway.rebuilds)
	}

	runtimeNode, _ := nodeMgr.Get(ctx, n.ID)
	if len(runtimeNode.Tunnels) != 1 || runtimeNode.Tunnels[0].Name != "api" {
		t.Fatalf("runtime tunnels not updated: %+v", runtimeNode.Tunnels)
	}
	persisted, _ := repo.GetByID(n.ID)
	if len(persisted.Tunnels) != 1 || persisted.Tunnels[0].Name != "api" {
		t.Fatalf("persisted tunnels not updated: %+v", persisted.Tunnels)
	}
}

func TestLoadPersisted_PushFailureKeepsRuntimeState(t *testing.T) {
	ctx := context.Background()
	nodeMgr := node.NewShardedNodeManager(4)
	repo := newMockNodeRepo()
	gateway := &mockGateway{}
	pusher := &mockPusher{err: core.ErrNodeOffline}
	svc := NewTunnelConfigService(nil, nodeMgr, repo, gateway, pusher, nil, nil)

	if err := nodeMgr.Add(ctx, &core.Node{
		ID:     "Node0003",
		Name:   "Node0003",
		Status: core.NodeStatusOnline,
		Tunnels: []core.Tunnel{
			{Name: "runtime", Type: core.TunnelTypeHTTP, Target: "127.0.0.1:8000"},
		},
	}); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if err := repo.Create(&core.Node{
		ID:     "Node0003",
		Name:   "Node0003",
		Status: core.NodeStatusOnline,
		Tunnels: []core.Tunnel{
			{Name: "persisted", Type: core.TunnelTypeHTTP, Target: "127.0.0.1:9000"},
		},
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if _, err := svc.LoadPersisted(ctx, "Node0003"); err == nil {
		t.Fatal("expected push failure to be returned")
	}

	runtimeNode, _ := nodeMgr.Get(ctx, "Node0003")
	if len(runtimeNode.Tunnels) != 1 || runtimeNode.Tunnels[0].Name != "runtime" {
		t.Fatalf("runtime tunnels should remain unchanged when load push fails: %+v", runtimeNode.Tunnels)
	}
	if gateway.rebuilds != 0 {
		t.Fatalf("gateway should not rebuild when load push fails, got %d", gateway.rebuilds)
	}
}

func TestLoadPersisted_SuccessRecoversRuntimeAfterReconnect(t *testing.T) {
	ctx := context.Background()
	nodeMgr := node.NewShardedNodeManager(4)
	repo := newMockNodeRepo()
	gateway := &mockGateway{}
	pusher := &mockPusher{}
	svc := NewTunnelConfigService(nil, nodeMgr, repo, gateway, pusher, nil, nil)

	if err := nodeMgr.Add(ctx, &core.Node{
		ID:      "Node0004",
		Name:    "Node0004",
		Status:  core.NodeStatusOnline,
		Tunnels: nil, // 模拟节点重连时客户端上报空配置
	}); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if err := repo.Create(&core.Node{
		ID:     "Node0004",
		Name:   "Node0004",
		Status: core.NodeStatusOffline,
		Tunnels: []core.Tunnel{
			{Name: "persisted", Type: core.TunnelTypeHTTP, Target: "127.0.0.1:7000"},
		},
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	loaded, err := svc.LoadPersisted(ctx, "Node0004")
	if err != nil {
		t.Fatalf("LoadPersisted failed: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Name != "persisted" {
		t.Fatalf("unexpected loaded tunnels: %+v", loaded)
	}
	if pusher.calls != 1 {
		t.Fatalf("expected one push call, got %d", pusher.calls)
	}
	if gateway.rebuilds != 1 {
		t.Fatalf("expected one rebuild after successful recovery, got %d", gateway.rebuilds)
	}

	runtimeNode, _ := nodeMgr.Get(ctx, "Node0004")
	if len(runtimeNode.Tunnels) != 1 || runtimeNode.Tunnels[0].Name != "persisted" {
		t.Fatalf("runtime tunnels not recovered from persistence: %+v", runtimeNode.Tunnels)
	}
}

func TestReplaceTunnels_DisabledTCPStopsListenerAndClearsStats(t *testing.T) {
	ctx := context.Background()
	nodeMgr := node.NewShardedNodeManager(4)
	repo := newMockNodeRepo()
	stats := &mockStatsReader{}
	gateway := &mockGateway{stats: stats}
	svc := NewTunnelConfigService(nil, nodeMgr, repo, gateway, nil, nil, nil)

	enabled := true
	disabled := false
	n := &core.Node{
		ID:     "Node0005",
		Name:   "Node0005",
		Status: core.NodeStatusOnline,
		Tunnels: []core.Tunnel{
			{Name: "ssh", Type: core.TunnelTypeTCP, Target: "127.0.0.1:22", Enabled: &enabled},
		},
	}
	if err := nodeMgr.Add(ctx, n); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	_, err := svc.ReplaceTunnels(ctx, n.ID, []core.Tunnel{
		{Name: "ssh", Type: core.TunnelTypeTCP, Target: "127.0.0.1:22", Enabled: &disabled},
	})
	if err != nil {
		t.Fatalf("ReplaceTunnels failed: %v", err)
	}

	if len(gateway.stopped) != 1 || gateway.stopped[0] != "ssh" {
		t.Fatalf("expected disabled tunnel listener to stop, got %+v", gateway.stopped)
	}
	if len(stats.removed) != 1 || stats.removed[0] != "Node0005/ssh" {
		t.Fatalf("expected disabled tunnel stats to be removed, got %+v", stats.removed)
	}

	runtimeNode, ok := nodeMgr.Get(ctx, n.ID)
	if !ok {
		t.Fatal("runtime node missing")
	}
	if len(runtimeNode.Tunnels) != 1 || runtimeNode.Tunnels[0].IsEnabled() {
		t.Fatalf("runtime tunnel should remain but be disabled: %+v", runtimeNode.Tunnels)
	}
}

// ===== p2p room 配对校验 =====

// p2pTunnel 构造带 room 的 p2p 隧道（para JSON 与客户端 P2PConfig 的 json tag 对齐）
func p2pTunnel(name, room string) core.Tunnel {
	para, _ := json.Marshal(map[string]any{"room": room, "protocol": "tcp"})
	return core.Tunnel{Name: name, Type: core.TunnelTypeP2P, Para: para}
}

// 配对不变量：同一 room 全局最多 2 条记录且分属 2 个不同节点。
// 依据：p2punch 假设 room 内恰两端（MQTT 首个响应者即配对），第三端持同 room
// 会与陌生节点完成 ECDHE 建连——等于把流量隧穿给陌生节点，必须挡在落库前。
func TestP2PRoomPairing(t *testing.T) {
	setup := func(t *testing.T) (*TunnelConfigService, *mockNodeRepo) {
		t.Helper()
		ctx := context.Background()
		nodeMgr := node.NewShardedNodeManager(4)
		repo := newMockNodeRepo()
		svc := NewTunnelConfigService(nil, nodeMgr, repo, &mockGateway{}, &mockPusher{}, nil, nil)
		for _, id := range []string{"Node0001", "Node0002", "Node0003"} {
			if err := nodeMgr.Add(ctx, &core.Node{ID: id, Name: id, Status: core.NodeStatusOffline}); err != nil {
				t.Fatalf("Add %s: %v", id, err)
			}
		}
		return svc, repo
	}

	t.Run("合法成对：两节点各一条同 room", func(t *testing.T) {
		svc, _ := setup(t)
		ctx := context.Background()
		if _, err := svc.ApplyTunnel(ctx, "Node0001", p2pTunnel("p2p-link", "roompair001")); err != nil {
			t.Fatalf("first side rejected: %v", err)
		}
		if _, err := svc.ApplyTunnel(ctx, "Node0002", p2pTunnel("p2p-link", "roompair001")); err != nil {
			t.Fatalf("second side rejected: %v", err)
		}
	})

	t.Run("第三条同 room 拒绝", func(t *testing.T) {
		svc, _ := setup(t)
		ctx := context.Background()
		_, _ = svc.ApplyTunnel(ctx, "Node0001", p2pTunnel("p2p-link", "roomfull001"))
		_, _ = svc.ApplyTunnel(ctx, "Node0002", p2pTunnel("p2p-link", "roomfull001"))
		if _, err := svc.ApplyTunnel(ctx, "Node0003", p2pTunnel("p2p-link", "roomfull001")); err == nil {
			t.Fatal("third same-room record must be rejected")
		}
	})

	t.Run("同节点两条同 room 拒绝", func(t *testing.T) {
		svc, _ := setup(t)
		ctx := context.Background()
		if _, err := svc.ApplyTunnel(ctx, "Node0001", p2pTunnel("p2p-a", "selfnode001")); err != nil {
			t.Fatalf("first tunnel rejected: %v", err)
		}
		if _, err := svc.ApplyTunnel(ctx, "Node0001", p2pTunnel("p2p-b", "selfnode001")); err == nil {
			t.Fatal("same-node duplicate room must be rejected")
		}
	})

	t.Run("更新自身不误判（排除自身计数）", func(t *testing.T) {
		svc, _ := setup(t)
		ctx := context.Background()
		if _, err := svc.ApplyTunnel(ctx, "Node0001", p2pTunnel("p2p-link", "selfupdat01")); err != nil {
			t.Fatalf("initial apply rejected: %v", err)
		}
		// 同名替换：room 不变，local_port 变更——不应被自己的旧记录误判为第三条
		if _, err := svc.ApplyTunnel(ctx, "Node0001", p2pTunnel("p2p-link", "selfupdat01")); err != nil {
			t.Fatalf("self-update rejected: %v", err)
		}
		// 旁边已有合法对端时，替换自身也必须放行
		if _, err := svc.ApplyTunnel(ctx, "Node0002", p2pTunnel("p2p-link", "selfupdat01")); err != nil {
			t.Fatalf("peer apply rejected: %v", err)
		}
		if _, err := svc.ApplyTunnel(ctx, "Node0001", p2pTunnel("p2p-link", "selfupdat01")); err != nil {
			t.Fatalf("self-update with peer rejected: %v", err)
		}
	})

	t.Run("SyncFromClient 与 ReplaceTunnels 同样受配对校验约束", func(t *testing.T) {
		svc, _ := setup(t)
		ctx := context.Background()
		if _, err := svc.ApplyTunnel(ctx, "Node0001", p2pTunnel("p2p-link", "syncpath001")); err != nil {
			t.Fatalf("seed apply rejected: %v", err)
		}
		if _, err := svc.ApplyTunnel(ctx, "Node0002", p2pTunnel("p2p-link", "syncpath001")); err != nil {
			t.Fatalf("peer apply rejected: %v", err)
		}
		if err := svc.SyncFromClient(ctx, "Node0003", []core.Tunnel{p2pTunnel("p2p-link", "syncpath001")}); err == nil {
			t.Fatal("SyncFromClient third same-room record must be rejected")
		}
		if _, err := svc.ReplaceTunnels(ctx, "Node0003", []core.Tunnel{p2pTunnel("p2p-link", "syncpath001")}); err == nil {
			t.Fatal("ReplaceTunnels third same-room record must be rejected")
		}
	})

	t.Run("删除一端后 room 可复用", func(t *testing.T) {
		svc, _ := setup(t)
		ctx := context.Background()
		_, _ = svc.ApplyTunnel(ctx, "Node0001", p2pTunnel("p2p-link", "reuseroom1"))
		_, _ = svc.ApplyTunnel(ctx, "Node0002", p2pTunnel("p2p-link", "reuseroom1"))
		if _, err := svc.RemoveTunnel(ctx, "Node0001", "p2p-link"); err != nil {
			t.Fatalf("remove: %v", err)
		}
		if _, err := svc.ApplyTunnel(ctx, "Node0003", p2pTunnel("p2p-link", "reuseroom1")); err != nil {
			t.Fatalf("room reuse after peer removal rejected: %v", err)
		}
	})
}
