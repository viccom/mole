package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"golang.org/x/crypto/ssh"

	"moleAgent_Serv/internal/auth"
	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/crypto"
	"moleAgent_Serv/internal/node"
	"moleAgent_Serv/internal/ratelimit"
)

type TunnelHandler struct {
	nodeMgr    *node.ShardedNodeManager
	tunnelSvc  core.TunnelConfigManager // 隧道配置单一变更入口
	stats      core.TunnelStatsReader   // 运行时统计读取
	limiter    ratelimit.GatewayLimiter // 限速配置读取
	encryptor  *crypto.SecretEncryptor  // 隧道凭证加密（nil=不加密）
	controlSrv ControlServer            // 控制端口（隧道操作）
}

type ControlServer interface {
	TriggerTunnelAction(ctx context.Context, nodeID, name, action string) error
}

func NewTunnelHandler(nodeMgr *node.ShardedNodeManager, tunnelSvc core.TunnelConfigManager, stats core.TunnelStatsReader, limiter ratelimit.GatewayLimiter, encryptor *crypto.SecretEncryptor, controlSrv ControlServer) *TunnelHandler {
	return &TunnelHandler{
		nodeMgr:    nodeMgr,
		tunnelSvc:  tunnelSvc,
		stats:      stats,
		limiter:    limiter,
		encryptor:  encryptor,
		controlSrv: controlSrv,
	}
}

func (h *TunnelHandler) List(w http.ResponseWriter, r *http.Request) {
	nodes := h.nodeMgr.GetAll(r.Context())
	type tunnelInfo struct {
		Name              string                `json:"name"`
		Type              string                `json:"type"`
		Target            string                `json:"target"`
		Domain            string                `json:"domain,omitempty"`
		ListenPort        int                   `json:"listen_port,omitempty"`
		Enabled           bool                  `json:"enabled"`
		NodeID            string                `json:"node_id"`
		Status            string                `json:"status"`
		Para              json.RawMessage       `json:"para,omitempty"`
		RateLimit         *core.TunnelRateLimit `json:"rate_limit,omitempty"`
		EffectiveMaxConns int                   `json:"effective_max_conns,omitempty"`
		EffectiveMaxBW    int64                 `json:"effective_max_bandwidth,omitempty"`
	}

	claims := auth.GetClaims(r.Context())
	isAdmin := IsAdmin(claims)

	items := make([]tunnelInfo, 0)
	for _, n := range nodes {
		if n.Status != core.NodeStatusOnline {
			continue
		}
		if !isAdmin && n.OwnerUserID != claims.UserID {
			continue
		}
		for _, t := range n.Tunnels {
			target := t.Target
			if target == "" && len(t.Para) > 0 {
				target = extractTargetFromPara(string(t.Type), t.Para)
			}
			var effConns int
			var effBW int64
			if h.limiter != nil {
				el := h.limiter.EffectiveLimits(n.ID, n.ID+"/"+t.Name)
				effConns = el.MaxConns
				effBW = el.MaxBandwidth
			}
			items = append(items, tunnelInfo{
				Name:              t.Name,
				Type:              string(t.Type),
				Target:            target,
				Domain:            t.Domain,
				ListenPort:        t.ListenPort,
				Enabled:           t.IsEnabled(),
				NodeID:            n.ID,
				Status:            "active",
				Para:              t.Para,
				RateLimit:         t.RateLimit,
				EffectiveMaxConns: effConns,
				EffectiveMaxBW:    effBW,
			})
		}
	}

	ResponseOK(w, map[string]any{
		"items": items,
		"total": len(items),
	})

}

func (h *TunnelHandler) Stats(w http.ResponseWriter, r *http.Request) {
	nodes := h.nodeMgr.GetAll(r.Context())

	// 归属过滤（无 claims 时视为管理员，与 checkNodeOwnership 放行惯例一致）
	claims := auth.GetClaims(r.Context())
	if claims != nil && !IsAdmin(claims) {
		filtered := make([]*core.Node, 0, len(nodes))
		for _, n := range nodes {
			if n.OwnerUserID == claims.UserID {
				filtered = append(filtered, n)
			}
		}
		nodes = filtered
	}

	totalTunnels := 0
	activeTunnels := 0
	tcpCount := 0
	udpCount := 0
	httpCount := 0
	httpsCount := 0
	ser2mqCount := 0
	vpnMgrCount := 0
	ser2tcpCount := 0
	ser2udpCount := 0
	p2pCount := 0

	for _, n := range nodes {
		totalTunnels += len(n.Tunnels)
		for _, t := range n.Tunnels {
			if n.Status == core.NodeStatusOnline && t.IsEnabled() {
				activeTunnels++
			}
			switch t.Type {
			case core.TunnelTypeTCP:
				tcpCount++
			case core.TunnelTypeUDP:
				udpCount++
			case core.TunnelTypeHTTP:
				httpCount++
			case core.TunnelTypeHTTPS:
				httpsCount++
			case "ser2mq":
				ser2mqCount++
			case "vpn-manager":
				vpnMgrCount++
			case "ser2tcp":
				ser2tcpCount++
			case "ser2udp":
				ser2udpCount++
			case "webssh":
				// webssh tunnels counted in total
			case core.TunnelTypeP2P:
				p2pCount++
			}
		}
	}

	enabledTunnels := 0
	for _, n := range nodes {
		for _, t := range n.Tunnels {
			if t.IsEnabled() {
				enabledTunnels++
			}
		}
	}

	ResponseOK(w, map[string]any{
		"total_tunnels":   totalTunnels,
		"enabled_tunnels": enabledTunnels,
		"active_tunnels":  activeTunnels,
		"tcp_tunnels":     tcpCount,
		"udp_tunnels":     udpCount,
		"http_tunnels":    httpCount,
		"https_tunnels":   httpsCount,
		"ser2mq_tunnels":  ser2mqCount,
		"vpn_mgr_tunnels": vpnMgrCount,
		"ser2tcp_tunnels": ser2tcpCount,
		"ser2udp_tunnels": ser2udpCount,
		"p2p_tunnels":     p2pCount,
	})
}

// Create 创建/更新隧道配置，通过 TunnelConfigService 统一处理
func (h *TunnelHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name           string                `json:"name"`
		Type           string                `json:"type"`
		Target         string                `json:"target"`
		Domain         string                `json:"domain,omitempty"`
		ListenPort     int                   `json:"listen_port,omitempty"`
		Enabled        *bool                 `json:"enabled,omitempty"`
		NodeID         string                `json:"node_id"`
		OriginalNodeID string                `json:"original_node_id,omitempty"`
		Para           json.RawMessage       `json:"para,omitempty"`
		RateLimit      *core.TunnelRateLimit `json:"rate_limit,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid request body")
		return
	}
	clientLocalTypes := map[string]bool{"ser2mq": true, "vpn-manager": true, "ser2tcp": true, "ser2udp": true, "webssh": true, "p2p": true}
	if req.Name == "" || req.Type == "" || req.NodeID == "" {
		ResponseError(w, http.StatusBadRequest, 400, "name, type, node_id are required")
		return
	}
	if req.Target == "" && !clientLocalTypes[req.Type] {
		ResponseError(w, http.StatusBadRequest, 400, "target is required")
		return
	}

	// 归属校验：非管理员只能给自己节点的隧道操作
	claims := auth.GetClaims(r.Context())
	if claims != nil && !IsAdmin(claims) {
		node, ok := h.nodeMgr.Get(r.Context(), req.NodeID)
		if !ok || node.OwnerUserID != claims.UserID {
			ResponseError(w, http.StatusNotFound, 404, "Node not found")
			return
		}
		// 迁移时还需校验旧节点归属
		if req.OriginalNodeID != "" && req.OriginalNodeID != req.NodeID {
			oldNode, ok := h.nodeMgr.Get(r.Context(), req.OriginalNodeID)
			if !ok || oldNode.OwnerUserID != claims.UserID {
				ResponseError(w, http.StatusNotFound, 404, "Original node not found")
				return
			}
		}
	}

	tunnelType := core.TunnelType(req.Type)
	switch tunnelType {
	case core.TunnelTypeHTTP, core.TunnelTypeHTTPS, core.TunnelTypeTCP, core.TunnelTypeUDP:
		// 标准隧道类型
	case "ser2mq", "vpn-manager", "ser2tcp", "ser2udp", "webssh":
		// 客户端本地类型，配置在 Para 字段中
	case core.TunnelTypeP2P:
		// p2p：客户端本地类型，target 不用（访问目标在 Para 由发起端指定）；
		// Para 校验（含 room 配对）由 TunnelConfigService.validateTunnel 统一把关
	default:
		ResponseError(w, http.StatusBadRequest, 400, "unsupported tunnel type: "+req.Type)
		return
	}

	newTunnel := core.Tunnel{
		Name:       req.Name,
		Type:       tunnelType,
		Target:     req.Target,
		Domain:     req.Domain,
		ListenPort: req.ListenPort,
		Enabled:    req.Enabled,
		RateLimit:  req.RateLimit,
		Para:       req.Para,
	}

	// 校验 webssh 配置（auth_type / 私钥格式），再加密凭证
	if tunnelType == "webssh" && len(req.Para) > 0 {
		if err := validateWebSSHPara(req.Para); err != nil {
			ResponseError(w, http.StatusBadRequest, 400, err.Error())
			return
		}
		if h.encryptor != nil {
			newTunnel.Para = encryptWebSSHPara(req.Para, h.encryptor)
		}
	}

	if h.tunnelSvc == nil {
		ResponseError(w, http.StatusInternalServerError, 500, "Tunnel service not configured")
		return
	}

	var result core.TunnelChangeResult
	var err error
	if req.OriginalNodeID != "" && req.OriginalNodeID != req.NodeID {
		result, err = h.tunnelSvc.MoveTunnel(r.Context(), req.OriginalNodeID, req.NodeID, newTunnel)
	} else {
		result, err = h.tunnelSvc.ApplyTunnel(r.Context(), req.NodeID, newTunnel)
	}
	if err != nil {
		if errors.Is(err, core.ErrNodeNotFound) {
			ResponseError(w, http.StatusNotFound, 404, "Node not found")
			return
		}
		if errors.Is(err, core.ErrTunnelInvalid) {
			ResponseError(w, http.StatusBadRequest, 400, err.Error())
			return
		}
		// REL-01：监听失败（端口被外部进程占用等）是可纠正的配置错误而非内部故障
		if errors.Is(err, core.ErrPortInUse) {
			ResponseError(w, http.StatusBadRequest, 400, "listen port is already in use")
			return
		}
		// QUA-02：内部错误细节（存储路径/驱动信息）进日志，对外 generic
		slog.Error("Failed to apply tunnel", "nodeId", req.NodeID, "tunnel", req.Name, "error", err)
		ResponseError(w, http.StatusInternalServerError, 500, "Failed to apply tunnel")
		return
	}
	ResponseOK(w, map[string]any{
		"status":        result.Status,
		"persisted":     result.Persisted,
		"client_synced": result.ClientSynced,
		"warning":       result.Warning,
		"tunnel":        newTunnel,
	})
}

// Delete 删除指定隧道，通过 TunnelConfigService 统一处理
func (h *TunnelHandler) Delete(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/v1/tunnels/")
	name = strings.TrimRight(name, "/")
	if name == "" {
		ResponseError(w, http.StatusBadRequest, 400, "Tunnel name required")
		return
	}

	// 查找拥有该隧道的节点
	claims := auth.GetClaims(r.Context())
	nodes := h.nodeMgr.GetAll(r.Context())
	var targetNodeID string
	found := false
	requestedNodeID := strings.TrimSpace(r.URL.Query().Get("node_id"))
	for _, n := range nodes {
		if !IsAdmin(claims) && n.OwnerUserID != claims.UserID {
			continue
		}
		if requestedNodeID != "" && n.ID != requestedNodeID {
			continue
		}
		for _, t := range n.Tunnels {
			if t.Name == name {
				targetNodeID = n.ID
				found = true
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		ResponseError(w, http.StatusNotFound, 404, "Tunnel not found")
		return
	}

	if h.tunnelSvc == nil {
		ResponseError(w, http.StatusInternalServerError, 500, "Tunnel service not configured")
		return
	}

	result, err := h.tunnelSvc.RemoveTunnel(r.Context(), targetNodeID, name)
	if err != nil {
		if errors.Is(err, core.ErrNodeNotFound) {
			ResponseError(w, http.StatusNotFound, 404, "Node not found")
			return
		}
		slog.Error("Failed to remove tunnel", "nodeId", targetNodeID, "tunnel", name, "error", err)
		ResponseError(w, http.StatusInternalServerError, 500, "Failed to remove tunnel")
		return
	}
	ResponseOK(w, map[string]any{
		"status":        result.Status,
		"removed":       name,
		"persisted":     result.Persisted,
		"client_synced": result.ClientSynced,
		"warning":       result.Warning,
	})
}

// Usage 返回隧道运行时使用情况（流量、连接数、活跃状态）
func (h *TunnelHandler) Usage(w http.ResponseWriter, r *http.Request) {
	nodes := h.nodeMgr.GetAll(r.Context())

	// 归属过滤
	claims := auth.GetClaims(r.Context())
	if claims != nil && !IsAdmin(claims) {
		filtered := make([]*core.Node, 0, len(nodes))
		for _, n := range nodes {
			if n.OwnerUserID == claims.UserID {
				filtered = append(filtered, n)
			}
		}
		nodes = filtered
	}

	// 查询参数过滤
	filterNodeID := r.URL.Query().Get("node_id")
	filterType := r.URL.Query().Get("type")
	filterStatus := r.URL.Query().Get("status")

	// 收集所有运行时统计
	var allStats map[string]*core.TunnelRuntimeStats
	if h.stats != nil {
		allStats = h.stats.GetAll()
	}

	type usageItem struct {
		Name              string                `json:"name"`
		Type              string                `json:"type"`
		Target            string                `json:"target"`
		Domain            string                `json:"domain,omitempty"`
		ListenPort        int                   `json:"listen_port,omitempty"`
		Enabled           bool                  `json:"enabled"`
		NodeID            string                `json:"node_id"`
		NodeStatus        string                `json:"node_status"`
		OwnerUserID       string                `json:"owner_user_id"`
		BytesIn           int64                 `json:"bytes_in"`
		BytesOut          int64                 `json:"bytes_out"`
		TotalConns        int64                 `json:"total_connections"`
		ActiveConns       int64                 `json:"active_connections"`
		LastActivity      string                `json:"last_activity,omitempty"`
		Para              json.RawMessage       `json:"para,omitempty"`
		RateLimit         *core.TunnelRateLimit `json:"rate_limit,omitempty"`
		EffectiveMaxConns int                   `json:"effective_max_conns,omitempty"`
		EffectiveMaxBW    int64                 `json:"effective_max_bandwidth,omitempty"`
	}

	items := make([]usageItem, 0)
	for _, n := range nodes {
		if filterNodeID != "" && n.ID != filterNodeID {
			continue
		}

		for _, t := range n.Tunnels {
			if filterType != "" && string(t.Type) != filterType {
				continue
			}

			target := t.Target
			if target == "" && len(t.Para) > 0 {
				target = extractTargetFromPara(string(t.Type), t.Para)
			}
			var effConns int
			var effBW int64
			if h.limiter != nil {
				el := h.limiter.EffectiveLimits(n.ID, n.ID+"/"+t.Name)
				effConns = el.MaxConns
				effBW = el.MaxBandwidth
			}
			item := usageItem{
				Name:              t.Name,
				Type:              string(t.Type),
				Target:            target,
				Domain:            t.Domain,
				ListenPort:        t.ListenPort,
				Enabled:           t.IsEnabled(),
				NodeID:            n.ID,
				NodeStatus:        string(n.Status),
				OwnerUserID:       n.OwnerUserID,
				Para:              t.Para,
				RateLimit:         t.RateLimit,
				EffectiveMaxConns: effConns,
				EffectiveMaxBW:    effBW,
			}

			// 关联运行时统计
			if allStats != nil {
				sKey := n.ID + "/" + t.Name
				if s, ok := allStats[sKey]; ok {
					item.BytesIn = s.BytesIn
					item.BytesOut = s.BytesOut
					item.TotalConns = s.TotalConns
					item.ActiveConns = s.ActiveConns
					item.LastActivity = s.LastActivity
				}
			}

			// 状态过滤
			if filterStatus == "active" && item.ActiveConns <= 0 {
				continue
			}

			items = append(items, item)
		}
	}

	ResponseOK(w, map[string]any{
		"items": items,
		"total": len(items),
	})
}

// extractTargetFromPara extracts display target from Para for types with empty Target field.
// VPN tunnels use the VPN provider key (vnt, easytier, tailscale, openvpn, etc.) as target.
func extractTargetFromPara(tunnelType string, para json.RawMessage) string {
	if tunnelType != "vpn-manager" {
		return ""
	}
	var p map[string]json.RawMessage
	if err := json.Unmarshal(para, &p); err != nil {
		return ""
	}
	vpnKeys := []string{"vnt", "easytier", "tailscale", "openvpn"}
	for _, key := range vpnKeys {
		if _, ok := p[key]; ok {
			return key
		}
	}
	return ""
}

// BatchRateLimit handles PATCH /api/v1/tunnels/rate-limit — batch update rate limits.
func (h *TunnelHandler) BatchRateLimit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Items []core.RateLimitItem `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid request body")
		return
	}
	if len(req.Items) == 0 {
		ResponseError(w, http.StatusBadRequest, 400, "items must not be empty")
		return
	}
	// Ownership check
	claims := auth.GetClaims(r.Context())
	if claims != nil && !IsAdmin(claims) {
		for _, item := range req.Items {
			node, ok := h.nodeMgr.Get(r.Context(), item.NodeID)
			if !ok || node.OwnerUserID != claims.UserID {
				ResponseError(w, http.StatusNotFound, 404, "Node not found: "+item.NodeID)
				return
			}
		}
	}
	results, err := h.tunnelSvc.BatchUpdateRateLimit(r.Context(), req.Items)
	if err != nil {
		// 审查③：service 层以 %w 包装哨兵（ErrTunnelInvalid/ErrNodeNotFound），
		// 必须 errors.Is 判定——== 比较对包装链是死分支
		if errors.Is(err, core.ErrTunnelInvalid) || errors.Is(err, core.ErrNodeNotFound) {
			ResponseError(w, http.StatusBadRequest, 400, err.Error())
			return
		}
		slog.Error("Failed to batch update rate limits", "error", err)
		ResponseError(w, http.StatusInternalServerError, 500, "Failed to update rate limits")
		return
	}
	ResponseOK(w, map[string]any{
		"updated": len(results),
		"items":   results,
	})
}

// Action handles POST /api/v1/tunnels/{name}/action — trigger start/stop/restart on a client tunnel.
func (h *TunnelHandler) Action(w http.ResponseWriter, r *http.Request) {
	// Extract tunnel name from path: /api/v1/tunnels/{name}/action
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/tunnels/")
	path = strings.TrimSuffix(path, "/action")
	name := strings.TrimRight(path, "/")
	if name == "" {
		ResponseError(w, http.StatusBadRequest, 400, "Tunnel name required")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB
	var req struct {
		Action string `json:"action"`
		NodeID string `json:"node_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid request body")
		return
	}
	if req.Action == "" || req.NodeID == "" {
		ResponseError(w, http.StatusBadRequest, 400, "action and node_id are required")
		return
	}
	switch req.Action {
	case "start", "stop", "restart":
	default:
		ResponseError(w, http.StatusBadRequest, 400, "action must be one of: start, stop, restart")
		return
	}

	// 归属检查
	claims := auth.GetClaims(r.Context())
	if claims != nil && !IsAdmin(claims) {
		node, ok := h.nodeMgr.Get(r.Context(), req.NodeID)
		if !ok || node.OwnerUserID != claims.UserID {
			ResponseError(w, http.StatusNotFound, 404, "Node not found")
			return
		}
	}

	if h.controlSrv == nil {
		ResponseError(w, http.StatusInternalServerError, 500, "Control server not configured")
		return
	}

	if err := h.controlSrv.TriggerTunnelAction(r.Context(), req.NodeID, name, req.Action); err != nil {
		slog.Error("TriggerTunnelAction failed", "node", req.NodeID, "tunnel", name, "action", req.Action, "error", err)
		ResponseError(w, http.StatusInternalServerError, 500, "Action failed")
		return
	}
	ResponseOK(w, map[string]any{"status": "ok", "action": req.Action, "tunnel": name})
}

// validateWebSSHPara 校验 webssh 隧道 Para 的合法性：
//   - auth_type 必须是 password 或 key
//   - key 认证时，明文 priv_key（非 enc: 密文）必须能被 ssh.ParsePrivateKey 解析，
//     避免误填公钥/损坏 PEM 直到客户端 SSH 握手才报错
//
// 密文 priv_key（enc: 前缀，编辑回显原样回传）跳过格式校验——首次创建时已校验。
// password 必填校验由客户端 Validate 兜底，这里不重复。
func validateWebSSHPara(para json.RawMessage) error {
	var m map[string]any
	if err := json.Unmarshal(para, &m); err != nil {
		return fmt.Errorf("invalid webssh para: %w", err)
	}
	authType, _ := m["auth_type"].(string)
	switch authType {
	case "password":
		// password 必填由客户端 Validate 兜底
	case "key":
		pk, _ := m["priv_key"].(string)
		if pk == "" {
			return fmt.Errorf("webssh: priv_key is required for key auth")
		}
		if crypto.IsEncrypted(pk) {
			return nil // 密文（编辑回显原样回传），首次创建已校验
		}
		if _, err := ssh.ParsePrivateKey([]byte(pk)); err != nil {
			return fmt.Errorf("webssh priv_key 不是合法私钥（是否误填了公钥？）: %w", err)
		}
	default:
		return fmt.Errorf("webssh: auth_type must be 'password' or 'key'")
	}
	return nil
}

// encryptWebSSHPara encrypts sensitive fields (password, priv_key) in webssh Para.
func encryptWebSSHPara(para json.RawMessage, enc *crypto.SecretEncryptor) json.RawMessage {
	var m map[string]any
	if err := json.Unmarshal(para, &m); err != nil {
		slog.Warn("encryptWebSSHPara: failed to unmarshal para", "error", err)
		return para
	}
	if v, ok := m["password"].(string); ok && v != "" && !crypto.IsEncrypted(v) {
		m["password"] = enc.Encrypt(v)
	}
	if v, ok := m["priv_key"].(string); ok && v != "" && !crypto.IsEncrypted(v) {
		m["priv_key"] = enc.Encrypt(v)
	}
	out, err := json.Marshal(m)
	if err != nil {
		slog.Warn("encryptWebSSHPara: failed to marshal para", "error", err)
		return para
	}
	return out
}
