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

		// listen_port 只对 TCP/UDP 生效：HTTP/HTTPS 走域名路由不占系统端口，
		// 零值合法；TCP/UDP 必须落在合法区间且避开服务自身保留端口（REL-01）
		if t.Type == core.TunnelTypeTCP || t.Type == core.TunnelTypeUDP {
			if err := core.ValidateTunnelListenPort(t.ListenPort); err != nil {
				return err
			}
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
	// listen_port -> 隧道名：TCP/UDP 网关监听是全局端口空间，
	// 同一次提交的列表内 listen_port 必须唯一（对齐隧道名唯一机制，REL-01）
	ports := make(map[int]string, len(tunnels))
	for i := range tunnels {
		if err := validateTunnel(tunnels[i]); err != nil {
			return err
		}
		if names[tunnels[i].Name] {
			return fmt.Errorf("%w: duplicate tunnel name %q", core.ErrTunnelInvalid, tunnels[i].Name)
		}
		names[tunnels[i].Name] = true
		if tunnels[i].Type == core.TunnelTypeTCP || tunnels[i].Type == core.TunnelTypeUDP {
			if prev, dup := ports[tunnels[i].ListenPort]; dup {
				return fmt.Errorf("%w: duplicate listen_port %d (tunnel %q and %q)", core.ErrTunnelInvalid, tunnels[i].ListenPort, prev, tunnels[i].Name)
			}
			ports[tunnels[i].ListenPort] = tunnels[i].Name
		}
	}
	return nil
}

const (
	maxConnsUpperBound           = 100000
	maxBandwidthUpperBound int64 = 10737418240 // 10 GB/s (显式 int64，避免 32-bit 平台 int 溢出)
)

// validateP2PRoomPairing 校验 p2p 隧道 room 的跨记录配对不变量：
// 同一 room 全局最多 2 条记录且分属 2 个不同节点。
// 依据：p2punch 假设 room 内恰两端（MQTT 首个响应者即配对），第三端持同 room
// 入场会与陌生节点完成 ECDHE 建连——等于把流量隧穿给陌生节点，必须挡在落库前。
// nodeID 是本次变更的节点：其旧记录整体被 updated 取代，扫描时排除（更新自身不误判）。
// 全部落库入口（ApplyTunnel / ReplaceTunnels / SyncFromClient / MoveTunnel）
// 经 applyTunnelChange(Locked) 统一调用。
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

// applyTunnelChange 配置变更的串行化核心：全程持 pairingMu，覆盖
// 「读持久层真相源→构建新列表→配对校验→落库」，返回（旧列表, 新列表, error）。
// 基线一律取持久层而非内存：内存分叉（推送失败历史/注册竞态产物）不应进入
// 写路径，且并发变更会以陈旧内存基线互相覆盖（复审 #4/#6/#14）；
// 锁范围覆盖基线读取到落库，收口配对扫描的 TOCTOU（审查 #3）。
func (s *TunnelConfigService) applyTunnelChange(ctx context.Context, nodeID string, build func(base []core.Tunnel) []core.Tunnel) ([]core.Tunnel, []core.Tunnel, error) {
	s.pairingMu.Lock()
	defer s.pairingMu.Unlock()
	return s.applyTunnelChangeLocked(ctx, nodeID, build)
}

// applyTunnelChangeLocked 为 applyTunnelChange 的不加锁内核
// （MoveTunnel 全序列临界区使用，复审 R7）
func (s *TunnelConfigService) applyTunnelChangeLocked(ctx context.Context, nodeID string, build func(base []core.Tunnel) []core.Tunnel) ([]core.Tunnel, []core.Tunnel, error) {
	base, err := s.persistedTunnels(ctx, nodeID)
	if err != nil {
		// 真 DB 错误中止变更：静默跳过会让吊销对照缺失（复审 R2）
		return nil, nil, err
	}
	next := build(base)
	if err := s.validateP2PRoomPairing(nodeID, next); err != nil {
		return nil, nil, err
	}
	if err := s.validateCrossNodeTunnelNames(ctx, nodeID, next); err != nil {
		return nil, nil, err
	}
	if err := s.persistUpdatedNode(ctx, nodeID, next); err != nil {
		return nil, nil, err
	}
	return base, next, nil
}

// validateCrossNodeTunnelNames 拒绝跨节点的同名启用 TCP/UDP 隧道。
// 监听器注册表与路由索引以隧道名为全局键：不同节点的同名 TCP/UDP 隧道
// 会互相杀掉对方监听器、并把外部流量路由到错误节点（跨租户串台）。
// 在写路径统一入口拒绝，把「静默互杀」变成显式配置错误。
func (s *TunnelConfigService) validateCrossNodeTunnelNames(ctx context.Context, nodeID string, next []core.Tunnel) error {
	isRoutable := func(t core.Tunnel) bool {
		return t.IsEnabled() && (t.Type == core.TunnelTypeTCP || t.Type == core.TunnelTypeUDP)
	}
	names := map[string]bool{}
	for _, t := range next {
		if isRoutable(t) {
			names[t.Name] = true
		}
	}
	if len(names) == 0 {
		return nil
	}
	conflict := func(other string, tunnels []core.Tunnel) error {
		for _, t := range tunnels {
			if names[t.Name] && isRoutable(t) {
				return fmt.Errorf("%w: TCP/UDP tunnel name %q already exists on node %s (gateway routing is keyed by tunnel name; cross-node duplicate names are not supported)",
					core.ErrTunnelInvalid, t.Name, other)
			}
		}
		return nil
	}
	if s.nodeRepo != nil {
		all, err := s.nodeRepo.GetAll()
		if err != nil {
			return fmt.Errorf("scan persisted nodes for tunnel name conflicts: %w", err)
		}
		for _, n := range all {
			if n == nil || n.ID == nodeID {
				continue
			}
			if err := conflict(n.ID, n.Tunnels); err != nil {
				return err
			}
		}
	}
	for _, n := range s.nodeMgr.GetAll(ctx) {
		if n == nil || n.ID == nodeID {
			continue
		}
		if err := conflict(n.ID, n.Tunnels); err != nil {
			return err
		}
	}
	return nil
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
	if n == nil {
		return nil, nil // 防约定外的 (nil, nil) 实现导致解引用（复审补）
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

	// 变更基线取持久层（复审 #4/#6）：追加或替换同名隧道
	oldTunnels, updated, err := s.applyTunnelChange(ctx, nodeID, func(base []core.Tunnel) []core.Tunnel {
		next := make([]core.Tunnel, 0, len(base)+1)
		replaced := false
		for _, t := range base {
			if t.Name == tunnelCfg.Name {
				next = append(next, tunnelCfg)
				replaced = true
			} else {
				next = append(next, t)
			}
		}
		if !replaced {
			next = append(next, tunnelCfg)
		}
		return next
	})
	if err != nil {
		return result, err
	}
	result.Persisted = true

	// 吊销跟随持久化真相源，且必须在推送提前返回之前执行（审查 #6）
	s.revokeRemovedP2PTokens(nodeID, oldTunnels, updated)

	// p2p 隧道创建即预签发信令凭据（客户端 p2p_signal_token 请求时按 owner 幂等命中）
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

	// 变更基线取持久层（复审 #4/#6）
	oldTunnels, updated, err := s.applyTunnelChange(ctx, nodeID, func(base []core.Tunnel) []core.Tunnel {
		next := make([]core.Tunnel, 0, len(base))
		for _, t := range base {
			if t.Name != tunnelName {
				next = append(next, t)
			}
		}
		return next
	})
	if err != nil {
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
	oldTunnels, _, err := s.applyTunnelChange(ctx, nodeID, func([]core.Tunnel) []core.Tunnel { return updated })
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
	oldTunnels, next, err := s.applyTunnelChange(ctx, nodeID, func(base []core.Tunnel) []core.Tunnel {
		return s.prepareClientSnapshot(base, tunnels)
	})
	if err != nil {
		return err
	}
	s.revokeRemovedP2PTokens(nodeID, oldTunnels, next)

	if err := s.applyRuntimeTunnels(ctx, nodeID, next); err != nil {
		return err
	}

	return nil
}

// prepareClientSnapshot 把客户端上报的隧道列表整形为可落库形态：
//  1. rate_limit 是服务端管理的真相字段，客户端快照缺失时按同名隧道从持久层继承
//     （旧版客户端根本不回传该字段，不合并则每次同步都会清空服务端限速配置；
//     显式清除走 REST 入口）
//  2. webssh 凭证落库必须保持密文：客户端持有的是服务端下发前解密的明文，
//     全量回传若原样落库，静态加密会被无声击穿（明文密码/私钥进 SQLite）
func (s *TunnelConfigService) prepareClientSnapshot(base, incoming []core.Tunnel) []core.Tunnel {
	baseRL := make(map[string]*core.TunnelRateLimit, len(base))
	for i := range base {
		if base[i].RateLimit != nil {
			baseRL[base[i].Name] = base[i].RateLimit
		}
	}
	next := make([]core.Tunnel, len(incoming))
	for i, t := range incoming {
		if t.RateLimit == nil {
			t.RateLimit = baseRL[t.Name]
		}
		if t.Type == core.TunnelTypeWebSSH && len(t.Para) > 0 && s.encryptor != nil {
			t.Para = encryptWebSSHParaForStorage(t.Para, s.encryptor)
		}
		next[i] = t
	}
	return next
}

// encryptWebSSHParaForStorage 落库前对 webssh Para 的敏感字段做加密
// （与 REST 入口 encryptWebSSHPara 语义一致，已加密字段原样保留）
func encryptWebSSHParaForStorage(para json.RawMessage, enc *crypto.SecretEncryptor) json.RawMessage {
	var m map[string]any
	if err := json.Unmarshal(para, &m); err != nil {
		return para // 非 JSON 原样保留，交给校验/下发路径报错
	}
	if v, ok := m["password"].(string); ok && v != "" && !crypto.IsEncrypted(v) {
		m["password"] = enc.Encrypt(v)
	}
	if v, ok := m["priv_key"].(string); ok && v != "" && !crypto.IsEncrypted(v) {
		m["priv_key"] = enc.Encrypt(v)
	}
	out, err := json.Marshal(m)
	if err != nil {
		return para
	}
	return out
}

// ActivateClientTunnels 仅把客户端上报配置激活到运行态（内存 tunnels +
// 网关监听器/路由索引），不落库。用于注册时 LoadPersisted 失败的兜底：
// 监听器立即可用，持久层保持不动，下次重连重试持久化加载。
func (s *TunnelConfigService) ActivateClientTunnels(ctx context.Context, nodeID string, tunnels []core.Tunnel) error {
	return s.applyRuntimeTunnels(ctx, nodeID, tunnels)
}

// IssueP2PTokenForTunnel 在持久层基线上校验 p2p 隧道存在后原子签发信令凭据。
// 校验与签发同一临界区（pairingMu）：旧实现「检查在锁外、签发在锁内」，
// 与 RemoveTunnel 的删除-吊销交错时可为已删除隧道产出孤儿凭据（R5 残余窗口）。
func (s *TunnelConfigService) IssueP2PTokenForTunnel(nodeID, tunnelName string) (string, string, int64, error) {
	s.pairingMu.Lock()
	defer s.pairingMu.Unlock()

	tunnels, err := s.persistedTunnels(context.Background(), nodeID)
	if err != nil {
		return "", "", 0, err
	}
	found := false
	for _, t := range tunnels {
		// 禁用隧道不签发：「禁用即切断信令」
		if t.Name == tunnelName && t.Type == core.TunnelTypeP2P && t.IsEnabled() {
			found = true
			break
		}
	}
	if !found {
		return "", "", 0, core.ErrTunnelNotFound
	}
	return s.p2pTokens.IssueP2PSignalToken(nodeID, tunnelName)
}

// LoadPersisted 节点注册后加载持久化隧道。
// 仅当客户端成功接受下发配置后，才切换服务端运行态到持久化配置。
func (s *TunnelConfigService) LoadPersisted(ctx context.Context, nodeID string) ([]core.Tunnel, error) {
	if s.nodeRepo == nil {
		return nil, nil
	}
	persisted, err := s.nodeRepo.GetByID(nodeID)
	if err != nil {
		if errors.Is(err, core.ErrNodeNotFound) {
			return nil, nil // 从未落库：合法「空」，注册流程走 SyncFromClient
		}
		// 真 DB 错误必须上抛：吞掉会让注册路径以客户端列表整表覆盖持久层，
		// 离线期间管理端新增的隧道被永久清除（R3 同类）
		return nil, fmt.Errorf("load persisted node %s: %w", nodeID, err)
	}
	if persisted == nil {
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
				MaxConns:     t.RateLimit.MaxConns,
				MaxBandwidth: t.RateLimit.MaxBandwidth,
			}
			s.limiter.UpdateTunnelConfig(sKey, cfg)
		} else {
			s.limiter.UpdateTunnelConfig(sKey, ratelimit.TunnelRateConfig{})
		}
	}

	// 清理已删除隧道的 override（不再出现在新 tunnel 列表中）
	{
		oldNames := make(map[string]bool, len(oldTunnels))
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
			var startErr error
			for i := range tunnels {
				t := tunnels[i]
				if !t.IsEnabled() {
					continue
				}
				if t.Type != core.TunnelTypeTCP && t.Type != core.TunnelTypeUDP {
					continue
				}
				// 已在监听同一端口的同名隧道无需重启：对同端口二次 bind 必然
				// 失败，历史上这类噪音错误被静默吞掉，上抛前必须先消除
				if s.isListenerActive(t) {
					continue
				}
				var err error
				switch t.Type {
				case core.TunnelTypeTCP:
					err = s.gateway.StartTCP(s.longLivedCtx, t)
				case core.TunnelTypeUDP:
					err = s.gateway.StartUDP(s.longLivedCtx, t)
				}
				if err != nil {
					slog.Error("Failed to start tunnel listener", "tunnel", t.Name, "type", t.Type, "error", err)
					if startErr == nil {
						startErr = fmt.Errorf("tunnel %s: %w", t.Name, err)
					}
					continue // 尽力启动其余隧道，失败汇总上抛
				}
			}
			if startErr != nil {
				// REL-01：监听失败必须上抛（ErrPortInUse 语义），不再静默继续。
				// StartTCP/StartUDP 失败不触碰监听器注册表，同名旧监听器仍在
				// 服务——配置失败可感知且既有服务不中断；跨隧道的已删除监听器
				// 按配置语义停止，不受此影响
				return fmt.Errorf("%w: %v", core.ErrPortInUse, startErr)
			}
		}
	}

	slog.Info("Node tunnels updated",
		"nodeId", nodeID,
		"tunnels", len(tunnels),
	)
	return nil
}

// isListenerActive 判断隧道的监听器是否已注册且仍在监听同一端口：
// 满足则跳过重复启动（避免对同端口二次 bind 的噪音失败），端口变更时
// 仍会走 StartTCP/StartUDP（新监听成功后由注册表替换关闭旧监听）
func (s *TunnelConfigService) isListenerActive(t core.Tunnel) bool {
	if s.gateway == nil {
		return false
	}
	reg := s.gateway.Registry()
	if reg == nil {
		return false
	}
	ln, ok := reg.Get(t.Name)
	if !ok {
		return false
	}
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		return false
	}
	portNum, err := strconv.Atoi(port)
	if err != nil {
		return false
	}
	return portNum == t.ListenPort
}

func (s *TunnelConfigService) persistUpdatedNode(ctx context.Context, nodeID string, tunnels []core.Tunnel) error {
	if s.nodeRepo == nil {
		return nil
	}
	// 落库模板优先取内存节点（带最新归属/限速等字段）；节点已下线时
	// nodeMgr 中无此记录（handleConnection 断开即 Remove），必须回退到持久层
	// 记录，否则「持久层有、内存无」的离线节点整条写路径不可达——
	// MoveTunnel 源侧基线已统一走持久层，此处失败会让离线节点迁移恒 500
	// （复审 R11：读基线改持久层但写回仍依赖内存，两者不一致）
	var persisted core.Node
	if node, ok := s.nodeMgr.Get(ctx, nodeID); ok {
		persisted = persistableNode(node)
	} else {
		existing, err := s.nodeRepo.GetByID(nodeID)
		if err != nil {
			if errors.Is(err, core.ErrNodeNotFound) {
				return core.ErrNodeNotFound
			}
			return fmt.Errorf("read persisted node %s: %w", nodeID, err)
		}
		if existing == nil {
			return core.ErrNodeNotFound
		}
		persisted = persistableNode(existing)
	}
	persisted.Tunnels = append([]core.Tunnel(nil), tunnels...)

	return s.nodeRepo.Update(&persisted)
}

// MoveTunnel 将隧道从一个节点迁移到另一个节点（原子操作：从旧节点删除 + 添加到新节点）。
func (s *TunnelConfigService) MoveTunnel(ctx context.Context, fromNodeID, toNodeID string, tunnelCfg core.Tunnel) (core.TunnelChangeResult, error) {
	if err := validateTunnel(tunnelCfg); err != nil {
		return core.TunnelChangeResult{}, err
	}
	result := core.TunnelChangeResult{Status: "ok"}

	// 移动整段进 pairingMu：源移除、目标配对、拒绝回滚必须同一临界区（复审 R7）；
	// 锁判据不限于 p2p 类型——非 p2p 移动进含 p2p 记录的目标同样要原子（复审 #5）
	s.pairingMu.Lock()
	defer s.pairingMu.Unlock()

	// 1. 从源节点移除：一律以持久层为基线（内存分叉不参与写路径，复审 #4/#6）。
	// 移动前读取的原列表既作吊销对照、又作失败回滚的还原内容
	fromOriginal, err := s.persistedTunnels(ctx, fromNodeID)
	if err != nil {
		return result, err
	}
	removedOldList := make([]core.Tunnel, 0, len(fromOriginal))
	for _, t := range fromOriginal {
		if t.Name != tunnelCfg.Name {
			removedOldList = append(removedOldList, t)
		}
	}
	removedFromOld := len(removedOldList) < len(fromOriginal)
	if removedFromOld {
		if err := s.persistUpdatedNode(ctx, fromNodeID, removedOldList); err != nil {
			return result, fmt.Errorf("persist old node after remove: %w", err)
		}
		if fromNode, ok := s.nodeMgr.Get(ctx, fromNodeID); ok {
			if err := s.applyRuntimeTunnels(ctx, fromNodeID, removedOldList); err != nil {
				slog.Warn("MoveTunnel: apply runtime for old node failed", "nodeId", fromNodeID, "error", err)
			}
			if fromNode.Status == core.NodeStatusOnline && s.pusher != nil {
				if err := s.pushToClient(ctx, fromNodeID, removedOldList); err != nil {
					slog.Warn("MoveTunnel: push to old node failed", "nodeId", fromNodeID, "error", err)
				}
			}
		}
	}

	// 2. 添加到新节点（配对校验 + 落库同锁，基线取持久层）
	newNode, newOk := s.nodeMgr.Get(ctx, toNodeID)
	if !newOk {
		s.rollbackOldNode(ctx, fromNodeID, fromOriginal, removedFromOld)
		return result, core.ErrNodeNotFound
	}
	destOld, updatedNew, err := s.applyTunnelChangeLocked(ctx, toNodeID, func(base []core.Tunnel) []core.Tunnel {
		next := make([]core.Tunnel, 0, len(base)+1)
		replaced := false
		for _, t := range base {
			if t.Name == tunnelCfg.Name {
				next = append(next, tunnelCfg)
				replaced = true
			} else {
				next = append(next, t)
			}
		}
		if !replaced {
			next = append(next, tunnelCfg)
		}
		return next
	})
	if err != nil {
		s.rollbackOldNode(ctx, fromNodeID, fromOriginal, removedFromOld)
		return result, err
	}
	result.Persisted = true

	// 凭据换主（提交后执行，失败回滚路径发生在任何吊销之前）：
	// 源侧消失的吊销、目标侧被同名替换掉的旧 p2p 隧道吊销（复审 #1/#2）、
	// 新配置预签发。
	if removedFromOld {
		s.revokeRemovedP2PTokens(fromNodeID, fromOriginal, removedOldList)
	}
	s.revokeRemovedP2PTokens(toNodeID, destOld, updatedNew)
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

// rollbackOldNode 回滚源节点到移动前的原始持久化配置（仅在移除已提交时执行）。
// 恢复内容必须是移动前读取的原列表——回写 incoming tunnelCfg 会把移动目标的
// room 落进源节点，让配对拒绝的回滚凭空造出第三条同 room 记录（复审 #1）。
func (s *TunnelConfigService) rollbackOldNode(ctx context.Context, fromNodeID string, original []core.Tunnel, removed bool) {
	if !removed {
		return
	}
	if _, ok := s.nodeMgr.Get(ctx, fromNodeID); ok {
		if rbErr := s.persistUpdatedNode(ctx, fromNodeID, original); rbErr != nil {
			slog.Error("MoveTunnel: rollback persist failed", "nodeId", fromNodeID, "error", rbErr)
			return
		}
		if rbErr := s.applyRuntimeTunnels(ctx, fromNodeID, original); rbErr != nil {
			slog.Error("MoveTunnel: rollback runtime failed", "nodeId", fromNodeID, "error", rbErr)
		}
		// 回滚必须回滚到客户端：源客户端已接受「移除」的 push，本地已删掉该隧道；
		// 不回推则服务端路由仍指向它而客户端无隧道可转发，直到重连才自愈
		if n, ok := s.nodeMgr.Get(ctx, fromNodeID); ok && n.Status == core.NodeStatusOnline && s.pusher != nil {
			if pErr := s.pushToClient(ctx, fromNodeID, original); pErr != nil {
				slog.Warn("MoveTunnel: rollback push to old node failed", "nodeId", fromNodeID, "error", pErr)
			}
		}
		return
	}
	// 离线节点：直接回写持久层（复审 R9：此前 !oldOk 直接 return 会让
	// 「移除成功→目标失败」的移动把隧道从持久层永久删除）
	if s.nodeRepo == nil {
		return
	}
	persisted, err := s.nodeRepo.GetByID(fromNodeID)
	if err != nil || persisted == nil {
		slog.Error("MoveTunnel: rollback read persisted node failed", "nodeId", fromNodeID, "error", err)
		return
	}
	persisted.Tunnels = original
	if err := s.nodeRepo.Update(persisted); err != nil {
		slog.Error("MoveTunnel: rollback persist (offline) failed", "nodeId", fromNodeID, "error", err)
	}
}

// persistableNode 返回面向持久化的节点副本：仅运行态的字段清零，
// 避免 sysinfo/客户端状态列表随每次落库写进 blob 持续膨胀、重启后
// 离线节点带着陈旧运行态"复活"；接入 token 同样剥离（SEC-03），
// 明文节点凭据不属于持久化契约
func persistableNode(n *core.Node) core.Node {
	cp := *n
	cp.SysInfo = nil
	cp.ClientStatuses = nil
	cp.RTT = 0
	cp.Token = ""
	return cp
}

// UpdateNodeRateLimit 更新节点级限速配置
func (s *TunnelConfigService) UpdateNodeRateLimit(ctx context.Context, nodeID string, rl *core.NodeRateLimit) error {
	if err := validateNodeRateLimit(rl); err != nil {
		return err
	}
	if _, ok := s.nodeMgr.Get(ctx, nodeID); !ok {
		return core.ErrNodeNotFound
	}
	// 持久化必须与隧道变更串行（pairingMu）且以持久层记录为基线：
	// 直接把内存节点整体写回 repo，会把隧道列表回滚成陈旧内存快照
	//（推送失败历史造成内存缺隧道），静默删除已落库隧道
	s.pairingMu.Lock()
	defer s.pairingMu.Unlock()

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
		persisted, err := s.nodeRepo.GetByID(nodeID)
		switch {
		case errors.Is(err, core.ErrNodeNotFound):
			// 尚无持久记录（如在线节点从未落库）：以运行态骨架创建，隧道列表为空
			if n, ok := s.nodeMgr.Get(ctx, nodeID); ok {
				cp := persistableNode(n)
				cp.Tunnels = nil
				cp.RateLimit = rl
				if err := s.nodeRepo.Create(&cp); err != nil {
					slog.Warn("Failed to persist node rate limit (create)", "nodeId", nodeID, "error", err)
				}
			}
		case err != nil:
			slog.Warn("Failed to read persisted node for rate limit update", "nodeId", nodeID, "error", err)
		case persisted != nil:
			persisted.RateLimit = rl
			if err := s.nodeRepo.Update(persisted); err != nil {
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
		// 构建基线必须取持久层：以内存快照整表回写会把持久层里
		// 内存缺失的隧道（推送失败历史）静默抹掉
		base, err := s.persistedTunnels(ctx, nodeID)
		if err != nil {
			return nil, fmt.Errorf("node %s: %w", nodeID, err)
		}
		// Build rate limit overrides map for this node
		overrides := make(map[string]*core.TunnelRateLimit, len(indices))
		for _, idx := range indices {
			overrides[items[idx].TunnelName] = items[idx].RateLimit
		}
		updated := make([]core.Tunnel, 0, len(base))
		seen := make(map[string]bool, len(base))
		for _, t := range base {
			if rl, ok := overrides[t.Name]; ok {
				t.RateLimit = rl
			}
			seen[t.Name] = true
			updated = append(updated, t)
		}
		// 持久层缺失但内存有、且本次要改限速的隧道：补入（避免改不到）
		if node, ok := s.nodeMgr.Get(ctx, nodeID); ok {
			for _, t := range node.Tunnels {
				if !seen[t.Name] {
					if rl, ok := overrides[t.Name]; ok {
						t.RateLimit = rl
					}
					updated = append(updated, t)
				}
			}
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
