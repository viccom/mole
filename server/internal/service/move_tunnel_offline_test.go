package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/node"
)

// TestMoveTunnel_FromNodeNotInMemory 复现生产事故：
// 把已下线（不在 nodeMgr 内存表、只存在于持久层）的节点上的隧道迁移到新节点。
// 报错 "Failed to apply tunnel: persist old node after remove: node not found"。
//
// 成因：5c15b63 把 MoveTunnel 的源侧基线统一改为持久层（persistedTunnels），
// 但 persistUpdatedNode 仍以 nodeMgr 内存为落库来源（s.nodeMgr.Get 失败即
// core.ErrNodeNotFound）。节点下线时 handleConnection 会把它从 nodeMgr 删除，
// 于是「持久层有、内存无」的离线节点必然命中该错误。
func TestMoveTunnel_FromNodeNotInMemory(t *testing.T) {
	ctx := context.Background()
	nodeMgr := node.NewShardedNodeManager(4)
	repo := newMockNodeRepo()
	svc := NewTunnelConfigService(nil, nodeMgr, repo, &mockGateway{}, &mockPusher{}, nil, nil)

	// 源节点：仅存在于持久层（模拟已下线且从 nodeMgr 移除）
	fromID := "Old00001"
	if err := repo.Create(&core.Node{
		ID: fromID, Name: fromID, Status: core.NodeStatusOffline,
		Tunnels: []core.Tunnel{
			{Name: "sp", Type: core.TunnelTypeHTTP, Target: "127.0.0.1:53000"},
			{Name: "keep", Type: core.TunnelTypeHTTP, Target: "127.0.0.1:53001"},
		},
	}); err != nil {
		t.Fatalf("seed old node: %v", err)
	}

	// 目标节点：在线且已在内存
	toID := "New00001"
	if err := nodeMgr.Add(ctx, &core.Node{ID: toID, Name: toID, Status: core.NodeStatusOnline}); err != nil {
		t.Fatalf("add new node: %v", err)
	}
	if err := repo.Create(&core.Node{ID: toID, Name: toID, Status: core.NodeStatusOnline}); err != nil {
		t.Fatalf("seed new node: %v", err)
	}

	_, err := svc.MoveTunnel(ctx, fromID, toID,
		core.Tunnel{Name: "sp", Type: core.TunnelTypeHTTP, Target: "127.0.0.1:53000"})

	if err != nil {
		if errors.Is(err, core.ErrNodeNotFound) || strings.Contains(err.Error(), "node not found") {
			t.Fatalf("从离线节点迁移失败（生产复现）: %v", err)
		}
		t.Fatalf("MoveTunnel failed: %v", err)
	}

	// 迁移应把隧道从源节点持久层移除、加到目标节点
	src, err := repo.GetByID(fromID)
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	for _, tn := range src.Tunnels {
		if tn.Name == "sp" {
			t.Fatalf("源节点仍留有 sp 隧道（迁移未生效）: %+v", src.Tunnels)
		}
	}
	dst, err := repo.GetByID(toID)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	found := false
	for _, tn := range dst.Tunnels {
		if tn.Name == "sp" {
			found = true
		}
	}
	if !found {
		t.Fatalf("目标节点未获得 sp 隧道: %+v", dst.Tunnels)
	}
	// 源节点其余隧道必须保留（不能被整体清除）
	kept := false
	for _, tn := range src.Tunnels {
		if tn.Name == "keep" {
			kept = true
		}
	}
	if !kept {
		t.Fatalf("源节点其他隧道被误删: %+v", src.Tunnels)
	}
}
