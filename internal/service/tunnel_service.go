package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"

	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/crypto"
	"moleAgent_Serv/internal/ratelimit"
	"moleAgent_Serv/internal/tunnel"
)

type routeIndexer interface {
	RebuildIndex(ctx context.Context)
	StartTCP(ctx context.Context, t core.Tunnel) error
	StartUDP(ctx context.Context, t core.Tunnel) error
	StopTunnel(name string)
	Registry() *tunnel.ListenerRegistry
	Stats() core.TunnelStatsReader
}

type tunnelPusher interface {
	PushTunnelUpdate(ctx context.Context, nodeID string, tunnels []core.Tunnel) error
}

// TunnelConfigService 隧道配置应用服务（单一变更入口）
type TunnelConfigService struct {
	nodeMgr   core.NodeManager
	nodeRepo  core.NodeRepo
	gateway   routeIndexer
	pusher    tunnelPusher
	limiter   ratelimit.GatewayLimiter
	encryptor *crypto.SecretEncryptor // nil = 不加解密
	p2pTokens *P2PSignalTokenService  // nil = 不签发/吊销 P2P 信令凭据
	// pairingMu 在变更候选含 p2p 隧道时串行化「配对校验→持久化」区间，
	// 封闭跨节点 room 扫描与落库之间的 TOCTOU 窗口（审查 #3）
	pairingMu sync.Mutex
	// longLivedCtx 是进程级 context，用于 TCP/UDP 监听器生命周期。
	// 不能用请求级 ctx（如 r.Context()），否则 HTTP 请求返回后监听器 ctx 级联取消，
	// 新连接的 sem.Acquire/waitBurst 立即失败，隧道建完即失效。
	longLivedCtx context.Context
}

// NewTunnelConfigService creates a tunnel config service.
func NewTunnelConfigService(longLivedCtx context.Context, nodeMgr core.NodeManager, nodeRepo core.NodeRepo, gateway routeIndexer, pusher tunnelPusher, limiter ratelimit.GatewayLimiter, encryptor *crypto.SecretEncryptor) *TunnelConfigService {
	if limiter == nil {
		limiter = ratelimit.NopLimiter{}
	}
	if longLivedCtx == nil {
		longLivedCtx = context.Background()
	}
	return &TunnelConfigService{
		longLivedCtx: longLivedCtx,
		nodeMgr:      nodeMgr,
		nodeRepo:     nodeRepo,
		gateway:      gateway,
		pusher:       pusher,
		limiter:      limiter,
		encryptor:    encryptor,
	}
}

// SetP2PSignalTokenService 注入 P2P 信令凭据签发服务（main.go 装配；nil = 不签发）
func (s *TunnelConfigService) SetP2PSignalTokenService(svc *P2PSignalTokenService) {
	s.p2pTokens = svc
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

	// 客户端本地类型：服务端不验证 target 格式，只做基本校验
	case "ser2mq", "vpn-manager", "ser2tcp", "ser2udp", "webssh":
		// 这些类型的配置在 Para 字段中，客户端自己处理
		// 服务端只需要确保 Name 不为空即可

	case core.TunnelTypeP2P:
		// p2p 与上述本地类型同类（仅客户端处理），但 room 是密钥材料，Para 必须严格校验
		if err := core.ValidateP2PPara(t.Para); err != nil {
			return err
		}

	default:
		return fmt.Errorf("%w: unknown tunnel type %q", core.ErrTunnelInvalid, t.Type)
	}

	if err := validateRateLimit(t.RateLimit); err != nil {
		return err
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

const (
	maxConnsUpperBound     = 100000
	maxBandwidthUpperBound int64 = 10737418240 // 10 GB/s (显式 int64，避免 32-bit 平台 int 溢出)
)

// validateP2PRoomPairing 校验 p2p 隧道 room 的跨记录配对不变量：
// 同一 room 全局最多 2 条记录且分属 2 个不同节点。
// 依据：p2punch 假设 room 内恰两端（MQTT 首个响应者即配对），第三端持同 room
// 入场会与陌生节点完成 ECDHE 建连——等于把流量隧穿给陌生节点，必须挡在落库前。
// nodeID 是本次变更的节点：其旧记录整体被 updated 取代，扫描时排除（更新自身不误判）。
// ApplyTunnel / ReplaceTunnels / SyncFromClient 三个落库入口均须调用。
func (s *TunnelConfigService) validateP2PRoomPairing(nodeID string, updated []core.Tunnel) error {
	if s.nodeRepo == nil {
		return nil
	}
	// 1. 本节点候选集内 room 不得重复（同节点两条同 room 拒绝）
	rooms := make(map[string]bool)
	for i := range updated {
		t := updated[i]
		if t.Type != core.TunnelTypeP2P {
			continue
		}
		room, err := core.P2PRoom(t.Para)
		if err != nil {
			continue // Para 非法由 validateTunnel 拦截，此处不重复报错
		}
		if rooms[room] {
			return fmt.Errorf("%w: p2p room %s already used by another tunnel on node %s", core.ErrTunnelInvalid, maskRoom(room), nodeID)
		}
		rooms[room] = true
	}
	if len(rooms) == 0 {
		return nil
	}
	// 2. 跨节点：同 room 在其他节点的既有记录须 ≤1（=0 开对，=1 成对，≥2 拒绝）
	others, err := s.nodeRepo.GetAll()
	if err != nil {
		return fmt.Errorf("scan nodes for p2p room pairing: %w", err)
	}
	counts := make(map[string]int, len(rooms))
	holder := make(map[string]string, len(rooms))
	for _, n := range others {
		if n.ID == nodeID {
			continue
		}
		for i := range n.Tunnels {
			t := n.Tunnels[i]
			if t.Type != core.TunnelTypeP2P {
				continue
			}
			room, err := core.P2PRoom(t.Para)
			if err != nil || !rooms[room] {
				continue
			}
			counts[room]++
			holder[room] = n.ID
		}
	}
	for room, c := range counts {
		if c > 1 {
			return fmt.Errorf("%w: p2p room %s already has %d records on other nodes (e.g. node %s)", core.ErrTunnelInvalid, maskRoom(room), c, holder[room])
		}
	}
	return nil
}

// maskRoom 日志/错误信息不回显完整 room（room 是共享密钥材料）
func maskRoom(room string) string {
	if len(room) <= 4 {
		return "***"
	}
	return room[:3] + "***" + room[len(room)-2:]
}

// validatePairingAndPersist 配对校验 + 持久化。候选集含 p2p 隧道时
// 「读旧列表→配对扫描→落库」整体在 pairingMu 互斥锁内完成——配对不变量
// （同 room 全局 ≤2 条）依赖扫描与落库的原子性，否则并发落库可各自通过
// 扫描造成 3 条记录（审查 #3）。非 p2p 变更不取锁，行为与原路径一致。
// 返回持久层旧列表（供吊销对照）。
func (s *TunnelConfigService) validatePairingAndPersist(ctx context.Context, nodeID string, candidate []core.Tunnel) ([]core.Tunnel, error) {
	hasP2P := false
	for _, t := range candidate {
		if t.Type == core.TunnelTypeP2P {
			hasP2P = true
			break
		}
	}
	if hasP2P {
		s.pairingMu.Lock()
		defer s.pairingMu.Unlock()
	}
	return s.validatePairingAndPersistLocked(ctx, nodeID, candidate)
}

// validatePairingAndPersistLocked 为 validatePairingAndPersist 的不加锁内核
// （调用方已持 pairingMu，如 MoveTunnel 的全序列临界区，复审 R7）
func (s *TunnelConfigService) validatePairingAndPersistLocked(ctx context.Context, nodeID string, candidate []core.Tunnel) ([]core.Tunnel, error) {
	oldTunnels, err := s.persistedTunnels(ctx, nodeID)
	if err != nil {
		// 真 DB 错误必须中止变更：静默跳过吊销对照 = 凭据带病存活到 TTL（复审 R2）
		return nil, err
	}
	if err := s.validateP2PRoomPairing(nodeID, candidate); err != nil {
		return nil, err
	}
	if err := s.persistUpdatedNode(ctx, nodeID, candidate); err != nil {
		return nil, err
	}
	return oldTunnels, nil
}

// preIssueP2PToken 预签发信令凭据：仅 p2p 且启用且已注入凭据服务时执行；
// 失败不阻断配置变更（客户端请求路径会再次签发）（审查 #8；复审合并两处重复块）
func (s *TunnelConfigService) preIssueP2PToken(nodeID string, t core.Tunnel) {
	if t.Type != core.TunnelTypeP2P || !t.IsEnabled() || s.p2pTokens == nil {
		return
	}
	if _, _, _, err := s.p2pTokens.IssueP2PSignalToken(nodeID, t.Name); err != nil {
		slog.Warn("Failed to pre-issue p2p signal token", "node", nodeID, "tunnel", t.Name, "error", err)
	}
}

// persistedTunnels 读持久化真相源的隧道列表。
// 吊销对照必须用它而非内存态：推送失败的既有路径会让内存缺隧道，
// 内存对照会拿到空列表而漏吊销（审查 #6/#7 的层分叉）。
// 真 DB 错误必须透传（复审 R2）；节点无持久记录（ErrNodeNotFound）才是合法「空」。
func (s *TunnelConfigService) persistedTunnels(ctx context.Context, nodeID string) ([]core.Tunnel, error) {
	if s.nodeRepo == nil {
		return nil, nil
	}
	n, err := s.nodeRepo.GetByID(nodeID)
	if err != nil {
		if errors.Is(err, core.ErrNodeNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("read persisted tunnels of node %s: %w", nodeID, err)
	}
	return n.Tunnels, nil
}

// revokeRemovedP2PTokens 对比新旧隧道列表，吊销从节点配置中「消失」的
// p2p 隧道信令凭据。消失 = 删除、禁用（Enabled=false 视同切断信令，与
// TCP/UDP 停监听语义一致）或被同名替换掉（审查 #8）。
//
// 调用时机契约：必须在 persistUpdatedNode 成功后、pushToClient 之前调用——
// 吊销跟随持久化真相源；若放在运行时同步路径，推送失败的提前返回会跳过它，
// 留下可继续签发的活凭据（审查 #6）。oldTunnels 为空视为服务重启后首次
// 加载（内存尚空），不误吊销既有凭据。
func (s *TunnelConfigService) revokeRemovedP2PTokens(nodeID string, oldTunnels, newTunnels []core.Tunnel) {
	if s.p2pTokens == nil || len(oldTunnels) == 0 {
		return
	}
	current := make(map[string]bool)
	for _, t := range newTunnels {
		if t.Type == core.TunnelTypeP2P && t.IsEnabled() {
			current[t.Name] = true
		}
	}
	revoked := make(map[string]bool)
	for _, t := range oldTunnels {
		if t.Type != core.TunnelTypeP2P || current[t.Name] || revoked[t.Name] {
			continue
		}
		revoked[t.Name] = true
		if err := s.p2pTokens.RevokeP2PSignalToken(nodeID, t.Name); err != nil {
			slog.Warn("Failed to revoke p2p signal token", "node", nodeID, "tunnel", t.Name, "error", err)
		}
	}
}

// validateRateLimit 校验限速配置（拒绝零值/负值/极大值）
func validateRateLimit(rl *core.TunnelRateLimit) error {
	if rl == nil {
		return nil
	}
	if rl.MaxConns < 0 || rl.MaxConns > maxConnsUpperBound {
		return fmt.Errorf("%w: max_conns must be 1-%d, got %d", core.ErrTunnelInvalid, maxConnsUpperBound, rl.MaxConns)
	}
	if rl.MaxBandwidth < 0 || rl.MaxBandwidth > maxBandwidthUpperBound {
		return fmt.Errorf("%w: max_bandwidth must be 1-%d bytes/sec, got %d", core.ErrTunnelInvalid, maxBandwidthUpperBound, rl.MaxBandwidth)
	}
	if rl.MaxConns == 0 && rl.MaxBandwidth == 0 {
		return fmt.Errorf("%w: rate_limit must have at least one non-zero field, use null to clear", core.ErrTunnelInvalid)
	}
	return nil
}

// validateNodeRateLimit 校验节点级限速配置
func validateNodeRateLimit(rl *core.NodeRateLimit) error {
	if rl == nil {
		return nil
	}
	if rl.MaxConns < 0 || rl.MaxConns > maxConnsUpperBound {
		return fmt.Errorf("%w: node max_conns must be 1-%d, got %d", core.ErrTunnelInvalid, maxConnsUpperBound, rl.MaxConns)
	}
	if rl.MaxConns == 0 {
		return fmt.Errorf("%w: node max_conns must be > 0, use null to clear", core.ErrTunnelInvalid)
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

	// p2p 候选在场时「读旧列表→配对→落库」同锁完成，封闭 TOCTOU 窗口（审查 #3）
	oldTunnels, err := s.validatePairingAndPersist(ctx, nodeID, updated)
	if err != nil {
		return result, err
	}
	result.Persisted = true

	// 吊销跟随持久化真相源，且必须在推送提前返回之前执行（审查 #6）
	s.revokeRemovedP2PTokens(nodeID, oldTunnels, updated)

	// p2p 隧道创建即预签发信令凭据（客户端 p2p_signal_token 请求时按 owner 幂等命中）；
	// 禁用的隧道不签发——「禁用即切断信令」与 TCP/UDP 停监听语义一致（审查 #8）
	s.preIssueP2PToken(nodeID, tunnelCfg)

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

	// 吊销对照读取失败必须中止（持久层尚未变更，天然一致）（复审 R2）
	oldTunnels, err := s.persistedTunnels(ctx, nodeID)
	if err != nil {
		return result, err
	}

	if err := s.persistUpdatedNode(ctx, nodeID, updated); err != nil {
		return result, err
	}
	result.Persisted = true

	// 吊销必须在推送提前返回之前执行（审查 #6）
	s.revokeRemovedP2PTokens(nodeID, oldTunnels, updated)

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
	// p2p 候选在场时「读旧列表→配对→落库」同锁完成，封闭 TOCTOU 窗口（审查 #3）
	oldTunnels, err := s.validatePairingAndPersist(ctx, nodeID, updated)
	if err != nil {
		return core.TunnelChangeResult{}, err
	}
	s.revokeRemovedP2PTokens(nodeID, oldTunnels, updated)
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
	// p2p 候选在场时「读旧列表→配对→落库」同锁完成，封闭 TOCTOU 窗口（审查 #3）
	oldTunnels, err := s.validatePairingAndPersist(ctx, nodeID, tunnels)
	if err != nil {
		return err
	}
	s.revokeRemovedP2PTokens(nodeID, oldTunnels, tunnels)

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
	if err != nil || persisted == nil {
		return nil, nil
	}
	// Restore node-level rate limit from persisted config
	if persisted.RateLimit != nil {
		s.limiter.UpdateNodeConfig(nodeID, ratelimit.NodeRateConfig{MaxConns: persisted.RateLimit.MaxConns})
		slog.Info("Restored node rate limit", "nodeId", nodeID, "maxConns", persisted.RateLimit.MaxConns)
	}
	if len(persisted.Tunnels) == 0 {
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
	for _, t := range tunnels {
		sKey := nodeID + "/" + t.Name
		if t.RateLimit != nil {
			cfg := ratelimit.TunnelRateConfig{
				MaxConns: t.RateLimit.MaxConns,
				MaxBandwidth:   t.RateLimit.MaxBandwidth,
			}
			s.limiter.UpdateTunnelConfig(sKey, cfg)
		} else {
			s.limiter.UpdateTunnelConfig(sKey, ratelimit.TunnelRateConfig{})
		}
	}

	// 清理已删除隧道的 override（不再出现在新 tunnel 列表中）
	{ oldNames := make(map[string]bool, len(oldTunnels))
	for _, t := range oldTunnels {
		oldNames[t.Name] = true
	}
	for _, t := range tunnels {
		delete(oldNames, t.Name)
	}
	for name := range oldNames {
		s.limiter.RemoveTunnel(nodeID + "/" + name)
	}
		}

	if s.gateway != nil {
		s.gateway.RebuildIndex(ctx)

		// 停止已删除/已禁用/已变更为非 TCP/UDP 的旧监听器，并清理其统计
		newTunnels := make(map[string]core.Tunnel, len(tunnels))
		for _, t := range tunnels {
			newTunnels[t.Name] = t
		}
		for _, oldT := range oldTunnels {
			if oldT.Type != core.TunnelTypeTCP && oldT.Type != core.TunnelTypeUDP {
				continue
			}

			newT, exists := newTunnels[oldT.Name]
			shouldStop := !exists || !newT.IsEnabled() || (newT.Type != core.TunnelTypeTCP && newT.Type != core.TunnelTypeUDP)
			if shouldStop {
				s.gateway.StopTunnel(oldT.Name)
				if s.gateway.Stats() != nil {
					s.gateway.Stats().Remove(nodeID + "/" + oldT.Name)
				}
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
					if err := s.gateway.StartTCP(s.longLivedCtx, t); err != nil {
						slog.Error("Failed to start TCP listener", "tunnel", t.Name, "error", err)
					}
				case core.TunnelTypeUDP:
					if err := s.gateway.StartUDP(s.longLivedCtx, t); err != nil {
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

// MoveTunnel 将隧道从一个节点迁移到另一个节点（原子操作：从旧节点删除 + 添加到新节点）。
func (s *TunnelConfigService) MoveTunnel(ctx context.Context, fromNodeID, toNodeID string, tunnelCfg core.Tunnel) (core.TunnelChangeResult, error) {
	if err := validateTunnel(tunnelCfg); err != nil {
		return core.TunnelChangeResult{}, err
	}
	result := core.TunnelChangeResult{Status: "ok"}

	// p2p 移动整段进 pairingMu：旧节点移除、目标节点配对、拒绝回滚必须同临界区，
	// 否则「移除→并发 Apply 挤入→移动拒绝→回滚」可残留 3 条同 room 记录（复审 R7）
	if tunnelCfg.Type == core.TunnelTypeP2P {
		s.pairingMu.Lock()
		defer s.pairingMu.Unlock()
	}

	// 1. 从旧节点移除隧道
	fromOldTunnels, pdErr := s.persistedTunnels(ctx, fromNodeID) // 吊销对照（持久层真相源）
	if pdErr != nil {
		return result, pdErr // 变更未开始，中止即一致（复审 R2）
	}
	oldNode, oldOk := s.nodeMgr.Get(ctx, fromNodeID)
	var oldTunnels []core.Tunnel

	removedFromOld := false
	var removedOldList []core.Tunnel // 旧节点移除后的列表（吊销对照，移动提交后才吊销）
	if oldOk {
		oldTunnels = oldNode.Tunnels
		updated := make([]core.Tunnel, 0, len(oldNode.Tunnels))
		for _, t := range oldNode.Tunnels {
			if t.Name != tunnelCfg.Name {
				updated = append(updated, t)
			}
		}

		if len(updated) < len(oldNode.Tunnels) {
			removedFromOld = true
			if err := s.persistUpdatedNode(ctx, fromNodeID, updated); err != nil {
				return result, fmt.Errorf("persist old node after remove: %w", err)
			}
			// 隧道已从旧节点持久配置消失；吊销延迟到移动提交后（回滚安全）
			removedOldList = updated
			if err := s.applyRuntimeTunnels(ctx, fromNodeID, updated); err != nil {
				slog.Warn("MoveTunnel: apply runtime for old node failed", "nodeId", fromNodeID, "error", err)
			}
			if oldNode.Status == core.NodeStatusOnline && s.pusher != nil {
				if err := s.pushToClient(ctx, fromNodeID, updated); err != nil {
					slog.Warn("MoveTunnel: push to old node failed", "nodeId", fromNodeID, "error", err)
				}
			}
		}
	}
	if !removedFromOld && s.nodeRepo != nil {
		// 旧节点不在内存，或内存与持久层分叉（推送失败历史导致内存缺隧道）：
		// 以持久化真相源执行移除（审查 #2/#7）
		persisted, err := s.nodeRepo.GetByID(fromNodeID)
		if err == nil && persisted != nil {
			updated := make([]core.Tunnel, 0, len(persisted.Tunnels))
			for _, t := range persisted.Tunnels {
				if t.Name != tunnelCfg.Name {
					updated = append(updated, t)
				}
			}
			oldTunnels = persisted.Tunnels
			if len(updated) < len(persisted.Tunnels) {
				persisted.Tunnels = updated
				if err := s.nodeRepo.Update(persisted); err != nil {
					slog.Warn("MoveTunnel: failed to persist old node removal", "nodeId", fromNodeID, "error", err)
				} else {
					removedFromOld = true
					removedOldList = updated
				}
			}
		}
	}

	// 2. 添加到新节点
	newNode, newOk := s.nodeMgr.Get(ctx, toNodeID)
	if !newOk {
		s.rollbackOldNode(ctx, fromNodeID, oldOk, oldTunnels, tunnelCfg)
		return result, core.ErrNodeNotFound
	}

	updatedNew := make([]core.Tunnel, 0, len(newNode.Tunnels)+1)
	replaced := false
	for _, t := range newNode.Tunnels {
		if t.Name == tunnelCfg.Name {
			updatedNew = append(updatedNew, tunnelCfg)
			replaced = true
		} else {
			updatedNew = append(updatedNew, t)
		}
	}
	if !replaced {
		updatedNew = append(updatedNew, tunnelCfg)
	}

	// 外层已持 pairingMu（p2p 时），用不加锁内核避免重入（审查 #2/#3 + 复审 R7）
	if _, err := s.validatePairingAndPersistLocked(ctx, toNodeID, updatedNew); err != nil {
		s.rollbackOldNode(ctx, fromNodeID, oldOk, oldTunnels, tunnelCfg)
		return result, err
	}
	result.Persisted = true

	// 凭据换主：移动提交后才吊销旧节点侧凭据——新节点持久化失败会回滚旧节点
	// 配置，此时凭据必须仍在（审查 #2）；预签发在提交后，失败路径无残留凭据。
	if removedFromOld {
		s.revokeRemovedP2PTokens(fromNodeID, fromOldTunnels, removedOldList)
	}
	s.preIssueP2PToken(toNodeID, tunnelCfg)

	// 在线新节点：推送 + 创建运行时
	if newNode.Status == core.NodeStatusOnline && s.pusher != nil {
		if err := s.pushToClient(ctx, toNodeID, updatedNew); err != nil {
			result.Status = "synced_server_only"
			result.Warning = "client push failed: " + err.Error()
			return result, nil
		}
		result.ClientSynced = true
	}

	if err := s.applyRuntimeTunnels(ctx, toNodeID, updatedNew); err != nil {
		return result, err
	}

	slog.Info("Tunnel moved", "tunnel", tunnelCfg.Name, "from", fromNodeID, "to", toNodeID)
	return result, nil
}

// rollbackOldNode 回滚旧节点：将隧道加回持久化 + 更新内存状态
func (s *TunnelConfigService) rollbackOldNode(ctx context.Context, fromNodeID string, oldOk bool, oldTunnels []core.Tunnel, tunnelCfg core.Tunnel) {
	if !oldOk {
		// 离线节点（仅持久层持有）：从持久层回滚。此前直接 return 会让
		// 「移除成功→目标节点失败」的移动把隧道从持久层永久删除（复审 R9）。
		if s.nodeRepo == nil {
			return
		}
		persisted, err := s.nodeRepo.GetByID(fromNodeID)
		if err != nil || persisted == nil {
			slog.Error("MoveTunnel: rollback read persisted node failed", "nodeId", fromNodeID, "error", err)
			return
		}
		rollback := make([]core.Tunnel, 0, len(persisted.Tunnels)+1)
		for _, t := range persisted.Tunnels {
			if t.Name != tunnelCfg.Name {
				rollback = append(rollback, t)
			}
		}
		rollback = append(rollback, tunnelCfg)
		persisted.Tunnels = rollback
		if err := s.nodeRepo.Update(persisted); err != nil {
			slog.Error("MoveTunnel: rollback persist (offline) failed", "nodeId", fromNodeID, "error", err)
		}
		return
	}
	rollback := make([]core.Tunnel, 0, len(oldTunnels)+1)
	for _, t := range oldTunnels {
		if t.Name != tunnelCfg.Name {
			rollback = append(rollback, t)
		}
	}
	rollback = append(rollback, tunnelCfg)
	if rbErr := s.persistUpdatedNode(ctx, fromNodeID, rollback); rbErr != nil {
		slog.Error("MoveTunnel: rollback persist failed", "nodeId", fromNodeID, "error", rbErr)
		return
	}
	if rbErr := s.applyRuntimeTunnels(ctx, fromNodeID, rollback); rbErr != nil {
		slog.Error("MoveTunnel: rollback runtime failed", "nodeId", fromNodeID, "error", rbErr)
	}
}

// UpdateNodeRateLimit 更新节点级限速配置
func (s *TunnelConfigService) UpdateNodeRateLimit(ctx context.Context, nodeID string, rl *core.NodeRateLimit) error {
	if err := validateNodeRateLimit(rl); err != nil {
		return err
	}
	_, ok := s.nodeMgr.Get(ctx, nodeID)
	if !ok {
		return core.ErrNodeNotFound
	}
	if err := s.nodeMgr.Update(ctx, nodeID, func(n *core.Node) {
		n.RateLimit = rl
	}); err != nil {
		return err
	}
	if rl != nil {
		s.limiter.UpdateNodeConfig(nodeID, ratelimit.NodeRateConfig{MaxConns: rl.MaxConns})
	} else {
		s.limiter.UpdateNodeConfig(nodeID, ratelimit.NodeRateConfig{})
	}
	if s.nodeRepo != nil {
		if n, ok := s.nodeMgr.Get(ctx, nodeID); ok {
			if err := s.nodeRepo.Update(n); err != nil {
				slog.Warn("Failed to persist node rate limit", "nodeId", nodeID, "error", err)
			}
		}
	}
	return nil
}

// BatchUpdateRateLimit 批量更新隧道限速（all-or-nothing）
func (s *TunnelConfigService) BatchUpdateRateLimit(ctx context.Context, items []core.RateLimitItem) ([]core.TunnelChangeResult, error) {
	if len(items) > 100 {
		return nil, fmt.Errorf("%w: batch size exceeds 100 items", core.ErrTunnelInvalid)
	}
	// Phase 1: validate all
	for _, item := range items {
		if item.NodeID == "" || item.TunnelName == "" {
			return nil, fmt.Errorf("%w: node_id and tunnel_name are required", core.ErrTunnelInvalid)
		}
		if err := validateRateLimit(item.RateLimit); err != nil {
			return nil, fmt.Errorf("%w: tunnel %s/%s: %v", core.ErrTunnelInvalid, item.NodeID, item.TunnelName, err)
		}
		node, ok := s.nodeMgr.Get(ctx, item.NodeID)
		if !ok {
			return nil, fmt.Errorf("node %s not found", item.NodeID)
		}
		found := false
		for _, t := range node.Tunnels {
			if t.Name == item.TunnelName {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("tunnel %s/%s not found", item.NodeID, item.TunnelName)
		}
	}
	// Phase 2: group by node, apply per-node once
	nodeItems := make(map[string][]int) // nodeID -> item indices
	for i, item := range items {
		nodeItems[item.NodeID] = append(nodeItems[item.NodeID], i)
	}
	results := make([]core.TunnelChangeResult, len(items))
	for nodeID, indices := range nodeItems {
		node, _ := s.nodeMgr.Get(ctx, nodeID)
		// Build rate limit overrides map for this node
		overrides := make(map[string]*core.TunnelRateLimit, len(indices))
		for _, idx := range indices {
			overrides[items[idx].TunnelName] = items[idx].RateLimit
		}
		updated := make([]core.Tunnel, 0, len(node.Tunnels))
		for _, t := range node.Tunnels {
			if rl, ok := overrides[t.Name]; ok {
				t.RateLimit = rl
			}
			updated = append(updated, t)
		}
		if _, err := s.ReplaceTunnels(ctx, nodeID, updated); err != nil {
			return nil, fmt.Errorf("node %s: %w", nodeID, err)
		}
		for _, idx := range indices {
			results[idx] = core.TunnelChangeResult{Status: "ok", Persisted: true}
		}
	}
	return results, nil
}

// ReleaseNodeResources 释放离线节点的隧道运行时资源（监听器、路由索引、统计条目）
// 不修改持久化配置，节点重连时可通过 applyRuntimeTunnels 重新激活
func (s *TunnelConfigService) ReleaseNodeResources(ctx context.Context, nodeID string, tunnels []core.Tunnel) {
	if s.gateway == nil {
		return
	}
	for _, t := range tunnels {
		if t.Type == core.TunnelTypeTCP || t.Type == core.TunnelTypeUDP {
			s.gateway.StopTunnel(t.Name)
		}
		sKey := nodeID + "/" + t.Name
		if s.gateway.Stats() != nil {
			s.gateway.Stats().Remove(sKey)
		}
		s.limiter.RemoveTunnel(sKey)
	}
	s.limiter.RemoveNode(nodeID)
	s.gateway.RebuildIndex(ctx)
	slog.Info("Released node tunnel resources", "nodeId", nodeID, "tunnels", len(tunnels))
}

// pushToClient 推送配置到客户端（同步，便于准确返回结果）
func (s *TunnelConfigService) pushToClient(ctx context.Context, nodeID string, tunnels []core.Tunnel) error {
	if s.pusher == nil {
		return nil
	}
	// 解密 webssh 凭证后再推送给客户端
	if s.encryptor != nil {
		decrypted := make([]core.Tunnel, len(tunnels))
		for i, t := range tunnels {
			decrypted[i] = t
			if t.Type == "webssh" && len(t.Para) > 0 {
				plain, err := decryptWebSSHPara(t.Para, s.encryptor)
				if err != nil {
					// 解密失败说明 secret 已变更或数据损坏：中止下发并明确报错，
					// 而非静默把密文当作凭证推给客户端（会导致 SSH 登录失败且难排查）。
					return fmt.Errorf("decrypt webssh para for tunnel %q on node %s: %w", t.Name, nodeID, err)
				}
				decrypted[i].Para = plain
			}
		}
		return s.pusher.PushTunnelUpdate(ctx, nodeID, decrypted)
	}
	return s.pusher.PushTunnelUpdate(ctx, nodeID, tunnels)
}

// decryptWebSSHPara decrypts sensitive fields in webssh Para for client delivery.
// Returns an error if any encrypted field fails to decrypt (secret mismatch / tampered),
// so the caller can fail loudly instead of silently shipping ciphertext as credentials.
func decryptWebSSHPara(para json.RawMessage, enc *crypto.SecretEncryptor) (json.RawMessage, error) {
	var m map[string]any
	if err := json.Unmarshal(para, &m); err != nil {
		return nil, fmt.Errorf("unmarshal para: %w", err)
	}
	if v, ok := m["password"].(string); ok && crypto.IsEncrypted(v) {
		decrypted, err := enc.Decrypt(v)
		if err != nil {
			return nil, fmt.Errorf("decrypt password: %w", err)
		}
		m["password"] = decrypted
	}
	if v, ok := m["priv_key"].(string); ok && crypto.IsEncrypted(v) {
		decrypted, err := enc.Decrypt(v)
		if err != nil {
			return nil, fmt.Errorf("decrypt priv_key: %w", err)
		}
		m["priv_key"] = decrypted
	}
	out, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshal para: %w", err)
	}
	return out, nil
}
