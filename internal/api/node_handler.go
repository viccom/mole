package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"moleAgent_Serv/internal/auth"
	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/node"
)

type NodeHandler struct {
	nodeMgr   *node.ShardedNodeManager
	nodeRepo  core.NodeRepo
	tunnelSvc core.TunnelConfigManager
}

func NewNodeHandler(nodeMgr *node.ShardedNodeManager, nodeRepo core.NodeRepo, tunnelSvc core.TunnelConfigManager) *NodeHandler {
	return &NodeHandler{nodeMgr: nodeMgr, nodeRepo: nodeRepo, tunnelSvc: tunnelSvc}
}

func (h *NodeHandler) List(w http.ResponseWriter, r *http.Request) {
	nodes := h.nodeMgr.GetAll(r.Context())

	// 归属过滤
	claims := auth.GetClaims(r.Context())
	if !IsAdmin(claims) {
		filtered := make([]*core.Node, 0, len(nodes))
		for _, n := range nodes {
			if n.OwnerUserID == claims.UserID {
				filtered = append(filtered, n)
			}
		}
		nodes = filtered
	}

	type nodeInfo struct {
		ID            string          `json:"id"`
		Name          string          `json:"name"`
		Status        core.NodeStatus `json:"status"`
		OwnerUserID   string          `json:"owner_user_id"`
		ConnectedAt   *string         `json:"connected_at,omitempty"`
		LastHeartbeat *string         `json:"last_heartbeat,omitempty"`
		TunnelCount   int             `json:"tunnel_count"`
		Tunnels       []core.Tunnel   `json:"tunnels"`
		RemoteAddr    string          `json:"remote_addr,omitempty"`
	}
	items := make([]nodeInfo, 0, len(nodes))
	for _, n := range nodes {
		ni := nodeInfo{
			ID:            n.ID,
			Name:          n.Name,
			Status:        n.Status,
			OwnerUserID:   n.OwnerUserID,
			TunnelCount:   len(n.Tunnels),
			Tunnels:       n.Tunnels,
			RemoteAddr:    n.RemoteAddr,
		}
		if n.ConnectedAt != nil {
			s := n.ConnectedAt.Format("2006-01-02T15:04:05Z")
			ni.ConnectedAt = &s
		}
		if n.LastHeartbeat != nil {
			s := n.LastHeartbeat.Format("2006-01-02T15:04:05Z")
			ni.LastHeartbeat = &s
		}
		items = append(items, ni)
	}
	ResponseOK(w, map[string]any{"items": items, "total": len(items)})
}

// Get handles GET /api/v1/nodes/{id} and GET /api/v1/nodes/{id}/tunnels
func (h *NodeHandler) Get(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/nodes/")

	if strings.HasSuffix(path, "/tunnels") {
		id := strings.TrimSuffix(path, "/tunnels")
		id = strings.TrimRight(id, "/")
		h.listTunnels(w, r, id)
		return
	}

	id := strings.TrimRight(path, "/")
	node, ok := h.nodeMgr.Get(r.Context(), id)
	if !ok {
		ResponseError(w, http.StatusNotFound, 404, "Node not found")
		return
	}
	if !checkNodeOwnership(w, r, node) {
		return
	}
	ResponseOK(w, node)
}

func (h *NodeHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string        `json:"name"`
		Token   string        `json:"token"`
		Tunnels []core.Tunnel `json:"tunnels"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid request body")
		return
	}
	if req.Name == "" {
		ResponseError(w, http.StatusBadRequest, 400, "Node name required")
		return
	}
	node := &core.Node{
		ID:      req.Name,
		Name:    req.Name,
		Token:   req.Token,
		Status:  core.NodeStatusOffline,
		Tunnels: req.Tunnels,
	}

	// 绑定归属：管理员创建的节点归属 system，普通用户归属自己
	claims := auth.GetClaims(r.Context())
	if claims != nil {
		if IsAdmin(claims) {
			node.OwnerUserID = "system"
		} else {
			node.OwnerUserID = claims.UserID
		}
	}
	if err := h.nodeMgr.Add(r.Context(), node); err != nil {
		ResponseError(w, http.StatusConflict, 409, "Node already exists")
		return
	}
	// Persist pre-configured node to Redka
	if err := h.nodeRepo.Create(node); err != nil {
		slog.Warn("Failed to persist node config", "error", err)
	}
	ResponseOK(w, node)
}

func (h *NodeHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/nodes/")
	id = strings.TrimRight(id, "/")

	var req struct {
		Name    string        `json:"name"`
		Tunnels []core.Tunnel `json:"tunnels"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid request body")
		return
	}

	// 归属检查
	if node, ok := h.nodeMgr.Get(r.Context(), id); ok {
		if !checkNodeOwnership(w, r, node) {
			return
		}
	}

	nameChanged := req.Name != ""

	// 非隧道字段使用显式 Update 方法，避免共享指针副作用。
	if err := h.nodeMgr.Update(r.Context(), id, func(n *core.Node) {
		if nameChanged {
			n.Name = req.Name
		}
	}); err != nil {
		ResponseError(w, http.StatusNotFound, 404, "Node not found")
		return
	}

	if req.Tunnels != nil {
		if h.tunnelSvc == nil {
			ResponseError(w, http.StatusInternalServerError, 500, "Tunnel service not configured")
			return
		}
		if _, err := h.tunnelSvc.ReplaceTunnels(r.Context(), id, req.Tunnels); err != nil {
			if err == core.ErrNodeNotFound {
				ResponseError(w, http.StatusNotFound, 404, "Node not found")
				return
			}
			if err == core.ErrTunnelInvalid {
				ResponseError(w, http.StatusBadRequest, 400, err.Error())
				return
			}
			ResponseError(w, http.StatusInternalServerError, 500, "Failed to update node tunnels: "+err.Error())
			return
		}
	}

	// 隧道更新由 TunnelConfigService 负责持久化；仅在纯节点属性更新时直接持久化。
	if req.Tunnels == nil && nameChanged {
		if node, ok := h.nodeMgr.Get(r.Context(), id); ok {
			if err := h.nodeRepo.Update(node); err != nil {
				slog.Warn("Failed to persist node update", "error", err)
			}
			ResponseOK(w, node)
			return
		}
		ResponseError(w, http.StatusNotFound, 404, "Node not found")
		return
	}

	if node, ok := h.nodeMgr.Get(r.Context(), id); ok {
		ResponseOK(w, node)
	} else {
		ResponseError(w, http.StatusNotFound, 404, "Node not found")
	}
}

// Delete handles DELETE /api/v1/nodes/{id} and DELETE /api/v1/nodes/{id}/connection
func (h *NodeHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/nodes/")

	if strings.HasSuffix(id, "/connection") {
		id = strings.TrimSuffix(id, "/connection")
		id = strings.TrimRight(id, "/")
		h.nodeMgr.Disconnect(r.Context(), id)
		ResponseOK(w, "disconnected")
		return
	}

	id = strings.TrimRight(id, "/")
	// 归属检查
	if node, ok := h.nodeMgr.Get(r.Context(), id); ok {
		if !checkNodeOwnership(w, r, node) {
			return
		}
	}
	h.nodeMgr.Disconnect(r.Context(), id)
	h.nodeRepo.Delete(id)
	ResponseOK(w, "deleted")
}

// ListTunnels handles GET /api/v1/nodes/{id}/tunnels (alternate entry)
func (h *NodeHandler) ListTunnels(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/nodes/")
	id = strings.TrimSuffix(id, "/tunnels")
	id = strings.TrimRight(id, "/")
	h.listTunnels(w, r, id)
}

func (h *NodeHandler) listTunnels(w http.ResponseWriter, r *http.Request, nodeID string) {
	node, ok := h.nodeMgr.Get(r.Context(), nodeID)
	if !ok {
		ResponseError(w, http.StatusNotFound, 404, "Node not found")
		return
	}
	if !checkNodeOwnership(w, r, node) {
		return
	}
	ResponseOK(w, node.Tunnels)
}
