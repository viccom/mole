package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/node"
	"moleAgent_Serv/internal/storage"
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
func (g *mockGateway) StopTunnel(name string)                          { g.stopped = append(g.stopped, name) }
func (g *mockGateway) Registry() *tunnel.ListenerRegistry              { return nil }
func (g *mockGateway) Stats() core.TunnelStatsReader                   { return g.stats }

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
	// 持久层真相源与内存一致（变更基线取持久层，复审 #4）
	if err := repo.Create(&core.Node{
		ID: online.ID, Name: online.Name, Status: core.NodeStatusOnline,
		Tunnels: []core.Tunnel{{Name: "old", Type: core.TunnelTypeHTTP, Target: "127.0.0.1:8080"}},
	}); err != nil {
		t.Fatalf("repo.Create failed: %v", err)
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

// ===== 审查 #6/#8：吊销跟随持久化真相源 + 禁用即吊销 =====

// p2pRevocationHarness 组装真实凭据服务的 svc（节点在线、推送必失败，
// 用于复现「提前返回跳过吊销」的路径）
type p2pRevocationHarness struct {
	svc     *TunnelConfigService
	tokens  *P2PSignalTokenService
	nodeMgr *node.ShardedNodeManager
	repo    *mockNodeRepo
}

func setupRevocationHarness(t *testing.T) *p2pRevocationHarness {
	t.Helper()
	nodeMgr := node.NewShardedNodeManager(4)
	repo := newMockNodeRepo()
	db := setupP2PTokenDB(t)
	tokens := NewP2PSignalTokenService(storage.NewP2PSignalTokenRepo(db))
	pusher := &mockPusher{err: errors.New("push down")}
	svc := NewTunnelConfigService(nil, nodeMgr, repo, &mockGateway{}, pusher, nil, nil)
	svc.SetP2PSignalTokenService(tokens)
	for _, id := range []string{"Node0001", "Node0002", "Node0003", "Node0004"} {
		if err := nodeMgr.Add(context.Background(), &core.Node{ID: id, Name: id, Status: core.NodeStatusOnline}); err != nil {
			t.Fatalf("Add %s: %v", id, err)
		}
	}
	return &p2pRevocationHarness{svc: svc, tokens: tokens, nodeMgr: nodeMgr, repo: repo}
}

// mustIssue 在线节点签发并返回 tokenID（推送失败不影响签发与持久化）
func (h *p2pRevocationHarness) mustIssue(t *testing.T, nodeID, name, room string) string {
	t.Helper()
	if _, err := h.svc.ApplyTunnel(context.Background(), nodeID, p2pTunnel(name, room)); err != nil {
		t.Fatalf("apply %s on %s: %v", name, nodeID, err)
	}
	tok, err := h.tokens.repo.FindByOwner(nodeID, name)
	if err != nil {
		t.Fatalf("token should exist after apply: %v", err)
	}
	return tok.TokenID
}

// 删除隧道时推送失败（提前返回 synced_server_only）也不得跳过吊销——
// 吊销必须跟随持久化真相源，而不是运行时同步路径（审查 #6）
func TestRevocationSurvivesPushFailure(t *testing.T) {
	h := setupRevocationHarness(t)

	tokenID := h.mustIssue(t, "Node0001", "p2p-a-b", "revpusher01")
	if _, err := h.svc.ApplyTunnel(context.Background(), "Node0002", p2pTunnel("p2p-a-b", "revpusher01")); err != nil {
		t.Fatalf("seed B: %v", err)
	}

	if _, err := h.svc.RemoveTunnel(context.Background(), "Node0001", "p2p-a-b"); err != nil {
		t.Fatalf("RemoveTunnel: %v", err)
	}
	if _, err := h.tokens.repo.FindByOwner("Node0001", "p2p-a-b"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("token must be revoked even when client push failed, find err = %v", err)
	}
	_ = tokenID
}

// ReplaceTunnels 把 p2p 隧道从列表移除（同一提前返回路径）也必须吊销
func TestReplaceTunnelsRemovalRevokes(t *testing.T) {
	h := setupRevocationHarness(t)

	h.mustIssue(t, "Node0001", "p2p-x", "reprevoked1")

	if _, err := h.svc.ReplaceTunnels(context.Background(), "Node0001", []core.Tunnel{}); err != nil {
		t.Fatalf("ReplaceTunnels: %v", err)
	}
	if _, err := h.tokens.repo.FindByOwner("Node0001", "p2p-x"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("token must be revoked when ReplaceTunnels removes the tunnel, find err = %v", err)
	}
}

// 禁用的 p2p 隧道视同消失：不预签发，禁用替换即吊销（审查 #8）
func TestDisabledP2PTunnelRevokedAndNotIssued(t *testing.T) {
	h := setupRevocationHarness(t)

	// 1) 直接创建禁用隧道：不得预签发
	disabled := false
	tun := p2pTunnel("p2p-off", "revdisabl1")
	tun.Enabled = &disabled
	if _, err := h.svc.ApplyTunnel(context.Background(), "Node0001", tun); err != nil {
		t.Fatalf("apply disabled: %v", err)
	}
	if _, err := h.tokens.repo.FindByOwner("Node0001", "p2p-off"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("disabled tunnel must not be pre-issued, find err = %v", err)
	}

	// 2) 先启用签发，再替换为禁用版本：凭据必须被吊销
	enabled := true
	tun2 := p2pTunnel("p2p-tog", "revtoggle1")
	tun2.Enabled = &enabled
	if _, err := h.svc.ApplyTunnel(context.Background(), "Node0001", tun2); err != nil {
		t.Fatalf("apply enabled: %v", err)
	}
	tok, ferr := h.tokens.repo.FindByOwner("Node0001", "p2p-tog")
	if ferr != nil {
		t.Fatalf("token should exist: %v", ferr)
	}

	tun2.Enabled = &disabled
	if _, err := h.svc.ReplaceTunnels(context.Background(), "Node0001", []core.Tunnel{tun2}); err != nil {
		t.Fatalf("replace with disabled: %v", err)
	}
	if _, err := h.tokens.repo.FindByOwner("Node0001", "p2p-tog"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("disabling must revoke the credential, find err = %v", err)
	}
	_ = tok
}

// ===== 审查 #2/#3：MoveTunnel 配对校验 + 并发 TOCTOU =====

// MoveTunnel 是第四个落库入口：配对校验必须覆盖，且合法移动要完成凭据换主
// （旧节点吊销、新节点预签发）（审查 #2）
func TestMoveTunnelP2PPairingAndTokens(t *testing.T) {
	h := setupRevocationHarness(t)
	c := context.Background()

	// 合法移动：room 只在 A，移到 Node0003
	h.mustIssue(t, "Node0001", "p2p-m", "movetoken1")
	if _, err := h.svc.MoveTunnel(c, "Node0001", "Node0003", p2pTunnel("p2p-m", "movetoken1")); err != nil {
		t.Fatalf("legal move rejected: %v", err)
	}
	if _, err := h.tokens.repo.FindByOwner("Node0001", "p2p-m"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("source token must be revoked after move, err = %v", err)
	}
	if _, err := h.tokens.repo.FindByOwner("Node0003", "p2p-m"); err != nil {
		t.Fatalf("target node must have pre-issued token, err = %v", err)
	}

	// 非法移动：room movelock01 已在 Node0002+Node0003 配对；把 A 上的
	// p2p-src（原 room srcroom001）移动并改成 room movelock01 移入 Node0004
	// → 候选将构成第 3 条记录 → 拒绝且回滚
	h.mustIssue(t, "Node0002", "p2p-p", "movelock01")
	h.mustIssue(t, "Node0003", "p2p-p", "movelock01")
	if _, err := h.svc.ApplyTunnel(c, "Node0001", p2pTunnel("p2p-src", "srcroom001")); err != nil {
		t.Fatalf("seed src: %v", err)
	}
	if _, err := h.svc.MoveTunnel(c, "Node0001", "Node0004", p2pTunnel("p2p-src", "movelock01")); err == nil {
		t.Fatal("moving a third record into a paired room must be rejected")
	}
	// 回滚后 A 的原隧道仍在（持久层），Node0004 无残留
	if _, err := h.tokens.repo.FindByOwner("Node0001", "p2p-src"); err != nil {
		t.Fatalf("source tunnel token must survive rollback, err = %v", err)
	}
	// 复审 #1 断言：回滚必须还原「移动前原列表」，不得把目标配置的 room
	// 写进源节点（否则 movelock01 会出现第 3 条持有记录）
	if n := countRoomHolders(t, h.repo, "movelock01"); n != 2 {
		t.Fatalf("movelock01 holders after rejected move = %d, want 2 (rollback must restore original room)", n)
	}
	srcRoom := tunnelRoomOf(t, h.repo, "Node0001", "p2p-src")
	if srcRoom != "srcroom001" {
		t.Fatalf("source tunnel room after rollback = %q, want original srcroom001 (复审 #1)", srcRoom)
	}
}

// countRoomHolders 统计持久层中持有指定 room 的 p2p 记录数
func countRoomHolders(t *testing.T, repo *mockNodeRepo, room string) int {
	t.Helper()
	nodes, err := repo.GetAll()
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	count := 0
	for _, n := range nodes {
		for _, tl := range n.Tunnels {
			if tl.Type == core.TunnelTypeP2P {
				if r, err := core.P2PRoom(tl.Para); err == nil && r == room {
					count++
				}
			}
		}
	}
	return count
}

// tunnelRoomOf 读取指定节点/隧道在持久层的 room
func tunnelRoomOf(t *testing.T, repo *mockNodeRepo, nodeID, name string) string {
	t.Helper()
	n, err := repo.GetByID(nodeID)
	if err != nil {
		t.Fatalf("GetByID(%s): %v", nodeID, err)
	}
	for _, tl := range n.Tunnels {
		if tl.Name == name {
			r, err := core.P2PRoom(tl.Para)
			if err != nil {
				t.Fatalf("P2PRoom: %v", err)
			}
			return r
		}
	}
	return ""
}

// 并发不变量：A 已持有 room R，B/C 并发申请同 room —— 最终全局 ≤2 条记录（审查 #3）
func TestConcurrentApplySameRoomInvariant(t *testing.T) {
	h := setupRevocationHarness(t)
	h.mustIssue(t, "Node0001", "p2p-a", "raceinvari1")

	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	targets := []string{"Node0002", "Node0003"}
	for i, target := range targets {
		wg.Add(1)
		go func(i int, target string) {
			defer wg.Done()
			<-start
			_, err := h.svc.ApplyTunnel(context.Background(), target, p2pTunnel("p2p-x", "raceinvari1"))
			mu.Lock()
			if err == nil {
				successes++
			}
			mu.Unlock()
		}(i, target)
	}
	close(start)
	wg.Wait()
	// 互斥语义：恰有一个并发申请者成功（复核 #15：仅断言 count≤2 时
	// 两个都失败或都成功都可能静默通过）
	if successes != 1 {
		t.Fatalf("exactly one concurrent applicant must succeed, got %d", successes)
	}

	// 扫持久层全节点统计持有该 room 的记录数
	count := 0
	nodes, err := h.repo.GetAll()
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	for _, n := range nodes {
		for _, tl := range n.Tunnels {
			if tl.Type != core.TunnelTypeP2P {
				continue
			}
			if room, err := core.P2PRoom(tl.Para); err == nil && room == "raceinvari1" {
				count++
			}
		}
	}
	if count > 2 {
		t.Fatalf("pairing invariant broken under concurrency: %d records hold the room, want <= 2", count)
	}
}

// ===== 复审第三轮回归 =====

// 移动替换掉目标节点的同名 p2p 隧道时，被替换者的凭据必须吊销死
// （复审 #2：仅换主源侧、不吊销目标侧旧凭据会留下 24h 可用孤儿）
func TestMoveTunnelReplacingDestinationP2PRevokesCredential(t *testing.T) {
	h := setupRevocationHarness(t)
	c := context.Background()

	// 目标节点 Node0003 持有 p2p 't1'（room destp2p01）并有凭据
	h.mustIssue(t, "Node0003", "t1", "destsame001")
	// 源节点 Node0001 持有一个同名但非 p2p 的隧道
	if _, err := h.svc.ApplyTunnel(c, "Node0001", core.Tunnel{
		Name: "t1", Type: core.TunnelTypeHTTP, Target: "127.0.0.1:8080",
	}); err != nil {
		t.Fatalf("seed http: %v", err)
	}

	// 把 HTTP 的 t1 移到 Node0003，同名替换掉其 p2p 记录
	if _, err := h.svc.MoveTunnel(c, "Node0001", "Node0003", core.Tunnel{
		Name: "t1", Type: core.TunnelTypeHTTP, Target: "127.0.0.1:8080",
	}); err != nil {
		t.Fatalf("move: %v", err)
	}
	if _, err := h.tokens.repo.FindByOwner("Node0003", "t1"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("replaced destination p2p credential must be revoked, find err = %v", err)
	}
}

// 变更基线取持久层：内存分叉（推送失败历史让内存缺隧道）不得让一次
// 无关变更把持久层里存在的隧道洗掉（复审 #4/#6）
func TestApplyTunnelPreservesPersistedOnlyTunnels(t *testing.T) {
	h := setupRevocationHarness(t)
	c := context.Background()

	// 持久层：节点持有 p2p-X（内存里没有——模拟推送失败分叉）
	if err := h.repo.Create(&core.Node{
		ID: "Node0001", Name: "Node0001", Status: core.NodeStatusOnline,
		Tunnels: []core.Tunnel{{Name: "p2p-x", Type: core.TunnelTypeP2P, Para: []byte(`{"room":"persistX01","protocol":"tcp"}`)}},
	}); err != nil {
		t.Fatalf("seed repo: %v", err)
	}

	// 施加一个无关变更
	if _, err := h.svc.ApplyTunnel(c, "Node0001", core.Tunnel{
		Name: "web", Type: core.TunnelTypeHTTP, Target: "127.0.0.1:80",
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	persisted, err := h.repo.GetByID("Node0001")
	if err != nil {
		t.Fatalf("repo read: %v", err)
	}
	found := false
	for _, tl := range persisted.Tunnels {
		if tl.Name == "p2p-x" {
			found = true
		}
	}
	if !found {
		t.Fatalf("persisted-only tunnel must survive unrelated apply, got %+v", persisted.Tunnels)
	}
}

// SEC-03：面向持久化的节点副本必须剥离接入 token——明文凭据不得进 blob
func TestPersistableNode_StripsToken(t *testing.T) {
	n := &core.Node{
		ID:     "Node0001",
		Name:   "n1",
		Token:  "secret-node-token",
		Status: core.NodeStatusOnline,
	}
	got := persistableNode(n)
	if got.Token != "" {
		t.Fatalf("persistableNode must strip Token, got %q", got.Token)
	}
	// 原节点不被就地篡改（调用方可能继续在内存态使用）
	if n.Token != "secret-node-token" {
		t.Fatalf("persistableNode must not mutate source node, got %q", n.Token)
	}
	// 其余持久化字段保持原样
	if got.ID != "Node0001" || got.Name != "n1" {
		t.Fatalf("unexpected persistableNode fields: %+v", got)
	}
}
