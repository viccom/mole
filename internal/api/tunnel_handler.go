package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"moleAgent_Serv/internal/auth"
	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/node"
)

type TunnelHandler struct {
	nodeMgr   *node.ShardedNodeManager
	tunnelSvc core.TunnelConfigManager // 隧道配置单一变更入口
	stats     core.TunnelStatsReader   // 运行时统计读取
}

func NewTunnelHandler(nodeMgr *node.ShardedNodeManager, tunnelSvc core.TunnelConfigManager, stats core.TunnelStatsReader) *TunnelHandler {
	return &TunnelHandler{
		nodeMgr:   nodeMgr,
		tunnelSvc: tunnelSvc,
		stats:     stats,
	}
}

func (h *TunnelHandler) List(w http.ResponseWriter, r *http.Request) {
	nodes := h.nodeMgr.GetAll(r.Context())
	type tunnelInfo struct {
		Name       string          `json:"name"`
		Type       string          `json:"type"`
		Target     string          `json:"target"`
		Domain     string          `json:"domain,omitempty"`
		ListenPort int             `json:"listen_port,omitempty"`
		Enabled    bool            `json:"enabled"`
		NodeID     string          `json:"node_id"`
		Status     string          `json:"status"`
		Para       json.RawMessage `json:"para,omitempty"`
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
			items = append(items, tunnelInfo{
				Name:       t.Name,
				Type:       string(t.Type),
				Target:     target,
				Domain:     t.Domain,
				ListenPort: t.ListenPort,
				Enabled:    t.IsEnabled(),
				NodeID:     n.ID,
				Status:     "active",
				Para:       t.Para,
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
		"tcp_tunnels":    tcpCount,
		"udp_tunnels":    udpCount,
		"http_tunnels":   httpCount,
		"https_tunnels":  httpsCount,
		"ser2mq_tunnels":  ser2mqCount,
		"vpn_mgr_tunnels": vpnMgrCount,
		"ser2tcp_tunnels": ser2tcpCount,
		"ser2udp_tunnels": ser2udpCount,
	})
}

// Create 创建/更新隧道配置，通过 TunnelConfigService 统一处理
func (h *TunnelHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name           string          `json:"name"`
		Type           string          `json:"type"`
		Target         string          `json:"target"`
		Domain         string          `json:"domain,omitempty"`
		ListenPort     int             `json:"listen_port,omitempty"`
		Enabled        *bool           `json:"enabled,omitempty"`
		NodeID         string          `json:"node_id"`
		OriginalNodeID string          `json:"original_node_id,omitempty"`
		Para           json.RawMessage `json:"para,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid request body")
		return
	}
	clientLocalTypes := map[string]bool{"ser2mq": true, "vpn-manager": true, "ser2tcp": true, "ser2udp": true}
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
	case "ser2mq", "vpn-manager", "ser2tcp", "ser2udp":
		// 客户端本地类型，配置在 Para 字段中
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
		Para:       req.Para,
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
		if err == core.ErrNodeNotFound {
			ResponseError(w, http.StatusNotFound, 404, "Node not found")
			return
		}
		if err == core.ErrTunnelInvalid {
			ResponseError(w, http.StatusBadRequest, 400, err.Error())
			return
		}
		ResponseError(w, http.StatusInternalServerError, 500, "Failed to apply tunnel: "+err.Error())
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
		if err == core.ErrNodeNotFound {
			ResponseError(w, http.StatusNotFound, 404, "Node not found")
			return
		}
		ResponseError(w, http.StatusInternalServerError, 500, "Failed to remove tunnel: "+err.Error())
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
		Name         string          `json:"name"`
		Type         string          `json:"type"`
		Target       string          `json:"target"`
		Domain       string          `json:"domain,omitempty"`
		ListenPort   int             `json:"listen_port,omitempty"`
		Enabled      bool            `json:"enabled"`
		NodeID       string          `json:"node_id"`
		NodeStatus   string          `json:"node_status"`
		OwnerUserID  string          `json:"owner_user_id"`
		BytesIn      int64           `json:"bytes_in"`
		BytesOut     int64           `json:"bytes_out"`
		TotalConns   int64           `json:"total_connections"`
		ActiveConns  int64           `json:"active_connections"`
		LastActivity string          `json:"last_activity,omitempty"`
		Para         json.RawMessage `json:"para,omitempty"`
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
			item := usageItem{
				Name:        t.Name,
				Type:        string(t.Type),
				Target:      target,
				Domain:      t.Domain,
				ListenPort:  t.ListenPort,
				Enabled:     t.IsEnabled(),
				NodeID:      n.ID,
				NodeStatus:  string(n.Status),
				OwnerUserID: n.OwnerUserID,
				Para:        t.Para,
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
