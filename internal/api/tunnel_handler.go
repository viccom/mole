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
	nodeMgr  *node.ShardedNodeManager
	gateway  *tunnel.TunnelGateway
}

func NewTunnelHandler(nodeMgr *node.ShardedNodeManager, gateway *tunnel.TunnelGateway) *TunnelHandler {
	return &TunnelHandler{nodeMgr: nodeMgr, gateway: gateway}
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

	tunnel := core.Tunnel{
		Name:       req.Name,
		Type:       tunnelType,
		Target:     req.Target,
		Domain:     req.Domain,
		ListenPort: req.ListenPort,
	}

	// 启动隧道
	if h.gateway != nil {
		var err error
		switch tunnelType {
		case core.TunnelTypeTCP:
			err = h.gateway.StartTCP(r.Context(), tunnel)
		case core.TunnelTypeUDP:
			err = h.gateway.StartUDP(r.Context(), tunnel)
		case core.TunnelTypeHTTP:
			err = h.gateway.RegisterHTTP(r.Context(), tunnel)
		}
		if err != nil {
			ResponseError(w, http.StatusInternalServerError, 500, "Failed to start tunnel: "+err.Error())
			return
		}
	}

	ResponseOK(w, tunnel)
}

func (h *TunnelHandler) Delete(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/v1/tunnels/")
	name = strings.TrimRight(name, "/")
	if name == "" {
		ResponseError(w, http.StatusBadRequest, 400, "Tunnel name required")
		return
	}

	if h.gateway != nil {
		h.gateway.StopTunnel(name)
	}

	ResponseOK(w, "tunnel stopped")
}
