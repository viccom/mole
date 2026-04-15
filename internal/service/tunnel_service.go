package service

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"

	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/tunnel"
)

type routeIndexer interface {
	RebuildIndex(ctx context.Context)
	StartTCP(ctx context.Context, t core.Tunnel) error
	StartUDP(ctx context.Context, t core.Tunnel) error
	StopTunnel(name string)
	Registry() *tunnel.ListenerRegistry
}

type tunnelPusher interface {
	PushTunnelUpdate(ctx context.Context, nodeID string, tunnels []core.Tunnel) error
}

// TunnelConfigService 隧道配置应用服务（单一变更入口）
// 统一处理：持久化、运行态更新、路由刷新、客户端同步
type TunnelConfigService struct {
	nodeMgr  core.NodeManager
	nodeRepo core.NodeRepo
	gateway  routeIndexer
	pusher   tunnelPusher
}

// NewTunnelConfigService 创建隧道配置服务
func NewTunnelConfigService(nodeMgr core.NodeManager, nodeRepo core.NodeRepo, gateway routeIndexer, pusher tunnelPusher) *TunnelConfigService {
	return &TunnelConfigService{
		nodeMgr:  nodeMgr,
		nodeRepo: nodeRepo,
		gateway:  gateway,
		pusher:   pusher,
	}
}

// validateTunnel 校验单条隧道配置的合法性
func validateTunnel(t core.Tunnel) error {
	if t.Name == "" {
		return fmt.Errorf("%w: tunnel name is required", core.ErrTunnelInvalid)
	}

	switch t.Type {
	case core.TunnelTypeHTTP, core.TunnelTypeHTTPS, core.TunnelTypeTCP, core.TunnelTypeUDP:
		// target 统一为 host:port 格式
		if t.Target == "" {
			return fmt.Errorf("%w: tunnel target is required", core.ErrTunnelInvalid)
		}
		host, port, err := net.SplitHostPort(t.Target)
		if err != nil {
			return fmt.Errorf("%w: tunnel target must be host:port format (e.g. 127.0.0.1:8080), got %q", core.ErrTunnelInvalid, t.Target)
		}
		if host == "" {
			return fmt.Errorf("%w: tunnel target host is required", core.ErrTunnelInvalid)
		}
		portNum, err := strconv.Atoi(port)
		if err != nil || portNum < 1 || portNum > 65535 {
			return fmt.Errorf("%w: tunnel target port must be 1-65535, got %q", core.ErrTunnelInvalid, port)
		}

	default:
		return fmt.Errorf("%w: unknown tunnel type %q", core.ErrTunnelInvalid, t.Type)
	}

	return nil
}

// validateTunnels 校验隧道列表
func validateTunnels(tunnels []core.Tunnel) error {
	names := make(map[string]bool, len(tunnels))
	for i := range tunnels {
		if err := validateTunnel(tunnels[i]); err != nil {
			return err
		}
		if names[tunnels[i].Name] {
			return fmt.Errorf("%w: duplicate tunnel name %q", core.ErrTunnelInvalid, tunnels[i].Name)
		}
		names[tunnels[i].Name] = true
	}
	return nil
}

// ApplyTunnel 添加或替换隧道配置
func (s *TunnelConfigService) ApplyTunnel(ctx context.Context, nodeID string, tunnelCfg core.Tunnel) (core.TunnelChangeResult, error) {
	if err := validateTunnel(tunnelCfg); err != nil {
		return core.TunnelChangeResult{}, err
	}
	result := core.TunnelChangeResult{Status: "ok"}
	node, ok := s.nodeMgr.Get(ctx, nodeID)
	if !ok {
		return result, core.ErrNodeNotFound
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

	if err := s.persistUpdatedNode(ctx, nodeID, updated); err != nil {
		return result, err
	}
	result.Persisted = true

	// 在线节点只有在客户端成功接收配置后，才更新服务端运行态索引，避免路由先切流导致业务异常。
	if node.Status == core.NodeStatusOnline && s.pusher != nil {
		if err := s.pushToClient(ctx, nodeID, updated); err != nil {
			result.Status = "synced_server_only"
			result.Warning = "client push failed: " + err.Error()
			return result, nil
		}
		result.ClientSynced = true
	}

	if err := s.applyRuntimeTunnels(ctx, nodeID, updated); err != nil {
		return result, err
	}

	return result, nil
}

// RemoveTunnel 删除隧道配置
func (s *TunnelConfigService) RemoveTunnel(ctx context.Context, nodeID string, tunnelName string) (core.TunnelChangeResult, error) {
	result := core.TunnelChangeResult{Status: "ok"}
	node, ok := s.nodeMgr.Get(ctx, nodeID)
	if !ok {
		return result, core.ErrNodeNotFound
	}

	// 过滤掉目标隧道
	updated := make([]core.Tunnel, 0, len(node.Tunnels))
	for _, t := range node.Tunnels {
		if t.Name != tunnelName {
			updated = append(updated, t)
		}
	}

	if err := s.persistUpdatedNode(ctx, nodeID, updated); err != nil {
		return result, err
	}
	result.Persisted = true

	if node.Status == core.NodeStatusOnline && s.pusher != nil {
		if err := s.pushToClient(ctx, nodeID, updated); err != nil {
			result.Status = "synced_server_only"
			result.Warning = "client push failed: " + err.Error()
			return result, nil
		}
		result.ClientSynced = true
	}

	if err := s.applyRuntimeTunnels(ctx, nodeID, updated); err != nil {
		return result, err
	}

	return result, nil
}

// ReplaceTunnels 用给定列表替换节点全部隧道配置，统一处理持久化、路由刷新和客户端同步。
func (s *TunnelConfigService) ReplaceTunnels(ctx context.Context, nodeID string, tunnels []core.Tunnel) (core.TunnelChangeResult, error) {
	if err := validateTunnels(tunnels); err != nil {
		return core.TunnelChangeResult{}, err
	}
	result := core.TunnelChangeResult{Status: "ok"}
	node, ok := s.nodeMgr.Get(ctx, nodeID)
	if !ok {
		return result, core.ErrNodeNotFound
	}

	updated := append([]core.Tunnel(nil), tunnels...)
	if err := s.persistUpdatedNode(ctx, nodeID, updated); err != nil {
		return result, err
	}
	result.Persisted = true

	if node.Status == core.NodeStatusOnline && s.pusher != nil {
		if err := s.pushToClient(ctx, nodeID, updated); err != nil {
			result.Status = "synced_server_only"
			result.Warning = "client push failed: " + err.Error()
			return result, nil
		}
		result.ClientSynced = true
	}

	if err := s.applyRuntimeTunnels(ctx, nodeID, updated); err != nil {
		return result, err
	}

	return result, nil
}

// SyncFromClient 客户端 tunnel_update 的统一处理入口
func (s *TunnelConfigService) SyncFromClient(ctx context.Context, nodeID string, tunnels []core.Tunnel) error {
	if err := validateTunnels(tunnels); err != nil {
		return err
	}
	if err := s.persistUpdatedNode(ctx, nodeID, tunnels); err != nil {
		return err
	}

	if err := s.applyRuntimeTunnels(ctx, nodeID, tunnels); err != nil {
		return err
	}

	return nil
}

// LoadPersisted 节点注册后加载持久化隧道。
// 仅当客户端成功接受下发配置后，才切换服务端运行态到持久化配置。
func (s *TunnelConfigService) LoadPersisted(ctx context.Context, nodeID string) ([]core.Tunnel, error) {
	if s.nodeRepo == nil {
		return nil, nil
	}
	persisted, err := s.nodeRepo.GetByID(nodeID)
	if err != nil || len(persisted.Tunnels) == 0 {
		return nil, nil
	}

	if err := s.pushToClient(ctx, nodeID, persisted.Tunnels); err != nil {
		return nil, err
	}
	if err := s.applyRuntimeTunnels(ctx, nodeID, persisted.Tunnels); err != nil {
		return nil, err
	}

	slog.Info("Pushed persisted tunnels to node",
		"nodeId", nodeID, "tunnels", len(persisted.Tunnels))

	return persisted.Tunnels, nil
}

func (s *TunnelConfigService) applyRuntimeTunnels(ctx context.Context, nodeID string, tunnels []core.Tunnel) error {
	// 获取旧隧道（Update 前获取，用于清理旧监听器）
	oldNode, _ := s.nodeMgr.Get(ctx, nodeID)
	var oldTunnels []core.Tunnel
	if oldNode != nil {
		oldTunnels = oldNode.Tunnels
	}

	if err := s.nodeMgr.Update(ctx, nodeID, func(n *core.Node) {
		n.Tunnels = append([]core.Tunnel(nil), tunnels...)
	}); err != nil {
		return err
	}

	if s.gateway != nil {
		s.gateway.RebuildIndex(ctx)

		// 停止已删除的 TCP/UDP 监听器
		newNames := make(map[string]bool, len(tunnels))
		for _, t := range tunnels {
			newNames[t.Name] = true
		}
		for _, t := range oldTunnels {
			if !newNames[t.Name] && (t.Type == core.TunnelTypeTCP || t.Type == core.TunnelTypeUDP) {
				s.gateway.StopTunnel(t.Name)
			}
		}

		// 在线节点：启动 TCP/UDP 监听器
		node, ok := s.nodeMgr.Get(ctx, nodeID)
		if ok && node.Status == core.NodeStatusOnline {
			for i := range tunnels {
				t := tunnels[i]
				if !t.IsEnabled() {
					continue
				}
				switch t.Type {
				case core.TunnelTypeTCP:
					if err := s.gateway.StartTCP(ctx, t); err != nil {
						slog.Error("Failed to start TCP listener", "tunnel", t.Name, "error", err)
					}
				case core.TunnelTypeUDP:
					if err := s.gateway.StartUDP(ctx, t); err != nil {
						slog.Error("Failed to start UDP listener", "tunnel", t.Name, "error", err)
					}
				}
			}
		}
	}

	slog.Info("Node tunnels updated",
		"nodeId", nodeID,
		"tunnels", len(tunnels),
	)
	return nil
}

func (s *TunnelConfigService) persistUpdatedNode(ctx context.Context, nodeID string, tunnels []core.Tunnel) error {
	if s.nodeRepo == nil {
		return nil
	}
	node, ok := s.nodeMgr.Get(ctx, nodeID)
	if !ok {
		return core.ErrNodeNotFound
	}

	persisted := *node
	persisted.Tunnels = append([]core.Tunnel(nil), tunnels...)

	existing, err := s.nodeRepo.GetByID(nodeID)
	if err != nil || existing == nil {
		if err := s.nodeRepo.Create(&persisted); err != nil {
			return err
		}
		return nil
	}
	return s.nodeRepo.Update(&persisted)
}

// pushToClient 推送配置到客户端（同步，便于准确返回结果）
func (s *TunnelConfigService) pushToClient(ctx context.Context, nodeID string, tunnels []core.Tunnel) error {
	if s.pusher == nil {
		return nil
	}
	return s.pusher.PushTunnelUpdate(ctx, nodeID, tunnels)
}
