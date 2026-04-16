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
		Name       string `json:"name"`
		Type       string `json:"type"`
		Target     string `json:"target"`
		Domain     string `json:"domain,omitempty"`
		ListenPort int    `json:"listen_port,omitempty"`
		Enabled    bool   `json:"enabled"`
		NodeID     string `json:"node_id"`
		Status     string `json:"status"`
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
			items = append(items, tunnelInfo{
				Name:       t.Name,
				Type:       string(t.Type),
				Target:     t.Target,
				Domain:     t.Domain,
				ListenPort: t.ListenPort,
				Enabled:    t.IsEnabled(),
				NodeID:     n.ID,
				Status:     "active",
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
	})
}

// Create 创建/更新隧道配置，通过 TunnelConfigService 统一处理
func (h *TunnelHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name       string `json:"name"`
		Type       string `json:"type"`
		Target     string `json:"target"`
		Domain     string `json:"domain,omitempty"`
		ListenPort int    `json:"listen_port,omitempty"`
		Enabled    *bool  `json:"enabled,omitempty"`
		NodeID     string `json:"node_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid request body")
		return
	}
	if req.Name == "" || req.Type == "" || req.Target == "" || req.NodeID == "" {
		ResponseError(w, http.StatusBadRequest, 400, "name, type, target, node_id are required")
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
	}

	tunnelType := core.TunnelType(req.Type)
	if tunnelType != core.TunnelTypeHTTP && tunnelType != core.TunnelTypeHTTPS && tunnelType != core.TunnelTypeTCP && tunnelType != core.TunnelTypeUDP {
		ResponseError(w, http.StatusBadRequest, 400, "type must be http, https, tcp, or udp")
		return
	}

	newTunnel := core.Tunnel{
		Name:       req.Name,
		Type:       tunnelType,
		Target:     req.Target,
		Domain:     req.Domain,
		ListenPort: req.ListenPort,
		Enabled:    req.Enabled,
	}

	if h.tunnelSvc == nil {
		ResponseError(w, http.StatusInternalServerError, 500, "Tunnel service not configured")
		return
	}

	result, err := h.tunnelSvc.ApplyTunnel(r.Context(), req.NodeID, newTunnel)
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
	for _, n := range nodes {
		if !IsAdmin(claims) && n.OwnerUserID != claims.UserID {
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
		Name         string `json:"name"`
		Type         string `json:"type"`
		Target       string `json:"target"`
		Domain       string `json:"domain,omitempty"`
		ListenPort   int    `json:"listen_port,omitempty"`
		Enabled      bool   `json:"enabled"`
		NodeID       string `json:"node_id"`
		NodeStatus   string `json:"node_status"`
		OwnerUserID  string `json:"owner_user_id"`
		BytesIn      int64  `json:"bytes_in"`
		BytesOut     int64  `json:"bytes_out"`
		TotalConns   int64  `json:"total_connections"`
		ActiveConns  int64  `json:"active_connections"`
		LastActivity string `json:"last_activity,omitempty"`
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

			item := usageItem{
				Name:        t.Name,
				Type:        string(t.Type),
				Target:      t.Target,
				Domain:      t.Domain,
				ListenPort:  t.ListenPort,
				Enabled:     t.IsEnabled(),
				NodeID:      n.ID,
				NodeStatus:  string(n.Status),
				OwnerUserID: n.OwnerUserID,
			}

			// 关联运行时统计
			if allStats != nil {
				if s, ok := allStats[t.Name]; ok {
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
