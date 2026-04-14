package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/node"
)

type TunnelHandler struct {
	nodeMgr   *node.ShardedNodeManager
	tunnelSvc core.TunnelConfigManager // 隧道配置单一变更入口
}

func NewTunnelHandler(nodeMgr *node.ShardedNodeManager, tunnelSvc core.TunnelConfigManager) *TunnelHandler {
	return &TunnelHandler{
		nodeMgr:   nodeMgr,
		tunnelSvc: tunnelSvc,
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
		NodeID     string `json:"node_id"`
		Status     string `json:"status"`
	}

	items := make([]tunnelInfo, 0)
	for _, n := range nodes {
		if n.Status != core.NodeStatusOnline {
			continue
		}
		for _, t := range n.Tunnels {
			items = append(items, tunnelInfo{
				Name:       t.Name,
				Type:       string(t.Type),
				Target:     t.Target,
				Domain:     t.Domain,
				ListenPort: t.ListenPort,
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
	totalTunnels := 0
	activeTunnels := 0
	tcpCount := 0
	udpCount := 0
	httpCount := 0

	for _, n := range nodes {
		totalTunnels += len(n.Tunnels)
		if n.Status == core.NodeStatusOnline {
			activeTunnels += len(n.Tunnels)
		}
		for _, t := range n.Tunnels {
			switch t.Type {
			case core.TunnelTypeTCP:
				tcpCount++
			case core.TunnelTypeUDP:
				udpCount++
			case core.TunnelTypeHTTP:
				httpCount++
			}
		}
	}

	ResponseOK(w, map[string]any{
		"total_tunnels":  totalTunnels,
		"active_tunnels": activeTunnels,
		"tcp_tunnels":    tcpCount,
		"udp_tunnels":    udpCount,
		"http_tunnels":   httpCount,
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

	tunnelType := core.TunnelType(req.Type)
	if tunnelType != core.TunnelTypeTCP && tunnelType != core.TunnelTypeUDP && tunnelType != core.TunnelTypeHTTP {
		ResponseError(w, http.StatusBadRequest, 400, "type must be tcp, udp, or http")
		return
	}

	newTunnel := core.Tunnel{
		Name:       req.Name,
		Type:       tunnelType,
		Target:     req.Target,
		Domain:     req.Domain,
		ListenPort: req.ListenPort,
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
	nodes := h.nodeMgr.GetAll(r.Context())
	var targetNodeID string
	found := false
	for _, n := range nodes {
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
