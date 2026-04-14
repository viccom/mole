package service

import (
	"context"
	"log/slog"

	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/tunnel"
)

// TunnelConfigService 隧道配置应用服务（单一变更入口）
// 统一处理：内存更新 → 持久化 → 路由刷新 → 客户端推送
type TunnelConfigService struct {
	nodeMgr    core.NodeManager
	nodeRepo   core.NodeRepo
	gateway    *tunnel.TunnelGateway
	controlSrv *tunnel.ControlServer
}

// NewTunnelConfigService 创建隧道配置服务
func NewTunnelConfigService(nodeMgr core.NodeManager, nodeRepo core.NodeRepo, gateway *tunnel.TunnelGateway, controlSrv *tunnel.ControlServer) *TunnelConfigService {
	return &TunnelConfigService{
		nodeMgr:    nodeMgr,
		nodeRepo:   nodeRepo,
		gateway:    gateway,
		controlSrv: controlSrv,
	}
}

// ApplyTunnel 添加或替换隧道配置
func (s *TunnelConfigService) ApplyTunnel(ctx context.Context, nodeID string, tunnelCfg core.Tunnel) error {
	node, ok := s.nodeMgr.Get(ctx, nodeID)
	if !ok {
		return core.ErrNodeNotFound
	}

	// 追加或替换同名隧道
	updated := make([]core.Tunnel, 0, len(node.Tunnels)+1)
	replaced := false
	for _, t := range node.Tunnels {
		if t.Name == tunnelCfg.Name {
			updated = append(updated, tunnelCfg)
			replaced = true
		} else {
			updated = append(updated, t)
		}
	}
	if !replaced {
		updated = append(updated, tunnelCfg)
	}

	// 更新内存态
	if err := s.nodeMgr.Update(ctx, nodeID, func(n *core.Node) {
		n.Tunnels = updated
	}); err != nil {
		return err
	}

	// 持久化
	s.persistNode(ctx, nodeID)

	// 刷新路由索引
	if s.gateway != nil {
		s.gateway.RebuildIndex(ctx)
	}

	// 推送客户端
	s.pushToClient(ctx, nodeID, updated)

	return nil
}

// RemoveTunnel 删除隧道配置
func (s *TunnelConfigService) RemoveTunnel(ctx context.Context, nodeID string, tunnelName string) error {
	node, ok := s.nodeMgr.Get(ctx, nodeID)
	if !ok {
		return core.ErrNodeNotFound
	}

	// 过滤掉目标隧道
	updated := make([]core.Tunnel, 0, len(node.Tunnels))
	for _, t := range node.Tunnels {
		if t.Name != tunnelName {
			updated = append(updated, t)
		}
	}

	// 更新内存态
	if err := s.nodeMgr.Update(ctx, nodeID, func(n *core.Node) {
		n.Tunnels = updated
	}); err != nil {
		return err
	}

	// 持久化
	s.persistNode(ctx, nodeID)

	// 刷新路由索引
	if s.gateway != nil {
		s.gateway.RebuildIndex(ctx)
	}

	// 推送客户端
	s.pushToClient(ctx, nodeID, updated)

	return nil
}

// SyncFromClient 客户端 tunnel_update 的统一处理入口
func (s *TunnelConfigService) SyncFromClient(ctx context.Context, nodeID string, tunnels []core.Tunnel) error {
	// 更新内存态
	if err := s.nodeMgr.Update(ctx, nodeID, func(n *core.Node) {
		n.Tunnels = tunnels
	}); err != nil {
		return err
	}

	slog.Info("Node tunnels updated",
		"nodeId", nodeID,
		"tunnels", len(tunnels),
	)

	// 持久化
	s.persistNode(ctx, nodeID)

	// 刷新路由索引
	if s.gateway != nil {
		s.gateway.RebuildIndex(ctx)
	}

	return nil
}

// LoadPersisted 节点注册后加载持久化隧道（覆盖客户端上报的空配置）
func (s *TunnelConfigService) LoadPersisted(ctx context.Context, nodeID string) ([]core.Tunnel, error) {
	if s.nodeRepo == nil {
		return nil, nil
	}
	persisted, err := s.nodeRepo.GetByID(nodeID)
	if err != nil || len(persisted.Tunnels) == 0 {
		return nil, nil
	}

	// 用持久化隧道覆盖内存
	if err := s.nodeMgr.Update(ctx, nodeID, func(n *core.Node) {
		n.Tunnels = persisted.Tunnels
	}); err != nil {
		return persisted.Tunnels, err
	}

	// 刷新路由索引
	if s.gateway != nil {
		s.gateway.RebuildIndex(ctx)
	}

	// 推送给客户端
	s.pushToClient(ctx, nodeID, persisted.Tunnels)

	slog.Info("Pushed persisted tunnels to node",
		"nodeId", nodeID, "tunnels", len(persisted.Tunnels))

	return persisted.Tunnels, nil
}

// persistNode 持久化节点到数据库
func (s *TunnelConfigService) persistNode(ctx context.Context, nodeID string) {
	if s.nodeRepo == nil {
		return
	}
	node, ok := s.nodeMgr.Get(ctx, nodeID)
	if !ok {
		return
	}
	existing, err := s.nodeRepo.GetByID(nodeID)
	if err != nil || existing == nil {
		if err := s.nodeRepo.Create(node); err != nil {
			slog.Debug("Failed to persist node (create)", "nodeId", nodeID, "error", err)
		}
	} else {
		if err := s.nodeRepo.Update(node); err != nil {
			slog.Debug("Failed to persist node (update)", "nodeId", nodeID, "error", err)
		}
	}
}

// pushToClient 推送配置到客户端（异步，不阻塞）
func (s *TunnelConfigService) pushToClient(ctx context.Context, nodeID string, tunnels []core.Tunnel) {
	if s.controlSrv == nil {
		return
	}
	go func() {
		if err := s.controlSrv.PushTunnelUpdate(ctx, nodeID, tunnels); err != nil {
			slog.Warn("Failed to push tunnels to client", "nodeId", nodeID, "error", err)
		}
	}()
}
