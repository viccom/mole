package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/node"
	"moleAgent_Serv/internal/tunnel"
)

type TunnelHandler struct {
	nodeMgr    *node.ShardedNodeManager
	gateway    *tunnel.TunnelGateway
	controlSrv *tunnel.ControlServer
}

func NewTunnelHandler(nodeMgr *node.ShardedNodeManager, gateway *tunnel.TunnelGateway, controlSrv *tunnel.ControlServer) *TunnelHandler {
	return &TunnelHandler{nodeMgr: nodeMgr, gateway: gateway, controlSrv: controlSrv}
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

// Create 创建/更新隧道配置，同步到客户端节点
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

	// 查找目标节点
	node, ok := h.nodeMgr.Get(r.Context(), req.NodeID)
	if !ok {
		ResponseError(w, http.StatusNotFound, 404, "Node not found or offline")
		return
	}

	// 更新节点的隧道配置：追加或替换同名隧道
	updated := make([]core.Tunnel, 0, len(node.Tunnels)+1)
	replaced := false
	for _, t := range node.Tunnels {
		if t.Name == req.Name {
			updated = append(updated, newTunnel)
			replaced = true
		} else {
			updated = append(updated, t)
		}
	}
	if !replaced {
		updated = append(updated, newTunnel)
	}

	// 更新节点内存中的隧道配置
	if err := h.nodeMgr.Update(r.Context(), req.NodeID, func(n *core.Node) {
		n.Tunnels = updated
	}); err != nil {
		ResponseError(w, http.StatusInternalServerError, 500, "Failed to update node: "+err.Error())
		return
	}

	// 重建路由索引
	if h.gateway != nil {
		h.gateway.RebuildIndex(r.Context())
	}

	// 推送配置到客户端
	if h.controlSrv != nil {
		if err := h.controlSrv.PushTunnelUpdate(r.Context(), req.NodeID, updated); err != nil {
			// 推送失败不影响服务端配置，记录日志即可
			ResponseOK(w, map[string]any{
				"status":  "synced_server_only",
				"warning": "client push failed: " + err.Error(),
				"tunnel":  newTunnel,
			})
			return
		}
	}

	ResponseOK(w, newTunnel)
}

// Delete 删除指定隧道，同步到客户端节点
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
	var updatedTunnels []core.Tunnel
	found := false

	for _, n := range nodes {
		if n.Status != core.NodeStatusOnline {
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
			// 构建不含该隧道的新列表
			node, _ := h.nodeMgr.Get(r.Context(), targetNodeID)
			if node != nil {
				for _, t := range node.Tunnels {
					if t.Name != name {
						updatedTunnels = append(updatedTunnels, t)
					}
				}
			}
			break
		}
	}

	if !found {
		// 也尝试停止服务端管理的隧道
		if h.gateway != nil {
			h.gateway.StopTunnel(name)
		}
		ResponseOK(w, "tunnel stopped")
		return
	}

	// 更新节点内存中的隧道配置
	if err := h.nodeMgr.Update(r.Context(), targetNodeID, func(n *core.Node) {
		n.Tunnels = updatedTunnels
	}); err != nil {
		ResponseError(w, http.StatusInternalServerError, 500, "Failed to update node: "+err.Error())
		return
	}

	// 重建路由索引
	if h.gateway != nil {
		h.gateway.RebuildIndex(r.Context())
	}

	// 推送配置到客户端
	if h.controlSrv != nil {
		h.controlSrv.PushTunnelUpdate(r.Context(), targetNodeID, updatedTunnels)
	}

	ResponseOK(w, map[string]any{"status": "ok", "removed": name})
}
