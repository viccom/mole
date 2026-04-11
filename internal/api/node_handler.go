package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/node"
)

type NodeHandler struct {
	nodeMgr  *node.ShardedNodeManager
	nodeRepo core.NodeRepo
}

func NewNodeHandler(nodeMgr *node.ShardedNodeManager, nodeRepo core.NodeRepo) *NodeHandler {
	return &NodeHandler{nodeMgr: nodeMgr, nodeRepo: nodeRepo}
}

func (h *NodeHandler) List(w http.ResponseWriter, r *http.Request) {
	nodes := h.nodeMgr.GetAll(r.Context())
	type nodeInfo struct {
		ID            string          `json:"id"`
		Name          string          `json:"name"`
		Status        core.NodeStatus `json:"status"`
		ConnectedAt   *string         `json:"connected_at,omitempty"`
		LastHeartbeat *string         `json:"last_heartbeat,omitempty"`
		TunnelCount   int             `json:"tunnel_count"`
		RemoteAddr    string          `json:"remote_addr,omitempty"`
	}
	items := make([]nodeInfo, 0, len(nodes))
	for _, n := range nodes {
		ni := nodeInfo{
			ID:          n.ID,
			Name:        n.Name,
			Status:      n.Status,
			TunnelCount: len(n.Tunnels),
			RemoteAddr:  n.RemoteAddr,
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

	node, ok := h.nodeMgr.Get(r.Context(), id)
	if !ok {
		ResponseError(w, http.StatusNotFound, 404, "Node not found")
		return
	}

	var req struct {
		Name    string        `json:"name"`
		Tunnels []core.Tunnel `json:"tunnels"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid request body")
		return
	}

	if req.Name != "" {
		node.Name = req.Name
	}
	if req.Tunnels != nil {
		node.Tunnels = req.Tunnels
	}

	// Save updated node back to in-memory manager
	h.nodeMgr.Add(r.Context(), node)
	// Persist to Redka
	if err := h.nodeRepo.Update(node); err != nil {
		slog.Warn("Failed to persist node update", "error", err)
	}

	ResponseOK(w, node)
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
	ResponseOK(w, node.Tunnels)
}
