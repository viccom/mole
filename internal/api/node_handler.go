package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"moleAgent_Serv/internal/auth"
	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/node"
)

type NodeHandler struct {
	nodeMgr    *node.ShardedNodeManager
	nodeRepo   core.NodeRepo
	tunnelSvc  core.TunnelConfigManager
	controlSrv NodeControlServer
	// p2pRevoker 节点删除时级联吊销 p2p 信令凭据（main.go 注入 service 实现；
	// nil = 跳过吊销）。独立小接口避免为窄关注点扩 core.TunnelConfigManager。
	p2pRevoker P2PTokenRevoker
}

type NodeControlServer interface {
	RestartNode(ctx context.Context, nodeID string, delay int, reason string) error
}

// P2PTokenRevoker 节点删除时的 p2p 信令凭据级联吊销（service 包实现）
type P2PTokenRevoker interface {
	RevokeNodeP2PTokens(nodeID string, tunnels []core.Tunnel)
}

// SetP2PTokenRevoker 注入凭据级联吊销器
func (h *NodeHandler) SetP2PTokenRevoker(v P2PTokenRevoker) {
	h.p2pRevoker = v
}

func NewNodeHandler(nodeMgr *node.ShardedNodeManager, nodeRepo core.NodeRepo, tunnelSvc core.TunnelConfigManager, controlSrv NodeControlServer) *NodeHandler {
	return &NodeHandler{nodeMgr: nodeMgr, nodeRepo: nodeRepo, tunnelSvc: tunnelSvc, controlSrv: controlSrv}
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
		ID             string                    `json:"id"`
		Name           string                    `json:"name"`
		Status         core.NodeStatus           `json:"status"`
		OwnerUserID    string                    `json:"owner_user_id"`
		ConnectedAt    *string                   `json:"connected_at,omitempty"`
		LastHeartbeat  *string                   `json:"last_heartbeat,omitempty"`
		TunnelCount    int                       `json:"tunnel_count"`
		Tunnels        []core.Tunnel             `json:"tunnels"`
		RemoteAddr     string                    `json:"remote_addr,omitempty"`
		SysInfo        *core.SysInfo             `json:"sysinfo,omitempty"`
		ClientStatuses []core.ClientTunnelStatus `json:"client_statuses,omitempty"`
		RTT            int64                     `json:"rtt,omitempty"`
	}
	items := make([]nodeInfo, 0, len(nodes))
	for _, n := range nodes {
		ni := nodeInfo{
			ID:             n.ID,
			Name:           n.Name,
			Status:         n.Status,
			OwnerUserID:    n.OwnerUserID,
			TunnelCount:    len(n.Tunnels),
			Tunnels:        n.Tunnels,
			RemoteAddr:     n.RemoteAddr,
			SysInfo:        n.SysInfo,
			ClientStatuses: n.ClientStatuses,
			RTT:            n.RTT,
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
		Name      string              `json:"name"`
		Token     string              `json:"token"`
		Tunnels   []core.Tunnel       `json:"tunnels"`
		RateLimit *core.NodeRateLimit `json:"rate_limit"`
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
		ID:        req.Name,
		Name:      req.Name,
		Token:     req.Token,
		Status:    core.NodeStatusOffline,
		Tunnels:   req.Tunnels,
		RateLimit: req.RateLimit,
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
		Name      string              `json:"name"`
		Tunnels   []core.Tunnel       `json:"tunnels"`
		RateLimit *core.NodeRateLimit `json:"rate_limit"`
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
		n.RateLimit = req.RateLimit
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
	if req.Tunnels == nil && (nameChanged || req.RateLimit != nil) {
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
	// 归属检查：先查内存，再查持久化；同时收集隧道列表供凭据级联吊销
	var tunnels []core.Tunnel
	if node, ok := h.nodeMgr.Get(r.Context(), id); ok {
		if !checkNodeOwnership(w, r, node) {
			return
		}
		tunnels = node.Tunnels
	} else if h.nodeRepo != nil {
		if persisted, err := h.nodeRepo.GetByID(id); err == nil && persisted != nil {
			if !checkNodeOwnership(w, r, persisted) {
				return
			}
			tunnels = persisted.Tunnels
		}
	}
	h.nodeMgr.Disconnect(r.Context(), id)
	h.nodeRepo.Delete(id)
	// 节点删除后 (nodeID, tunnelName) 定位不到凭据，必须在此显式吊销（审查 #5）
	if h.p2pRevoker != nil {
		h.p2pRevoker.RevokeNodeP2PTokens(id, tunnels)
	}
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

// ListPersisted handles GET /api/v1/nodes/persisted — returns all persisted nodes from the database.
// Used for managing offline node data: cleanup and tunnel migration.
func (h *NodeHandler) ListPersisted(w http.ResponseWriter, r *http.Request) {
	if h.nodeRepo == nil {
		ResponseOK(w, map[string]any{"items": []any{}, "total": 0})
		return
	}

	allPersisted, err := h.nodeRepo.GetAll()
	if err != nil {
		ResponseError(w, http.StatusInternalServerError, 500, "Failed to load persisted nodes")
		return
	}

	claims := auth.GetClaims(r.Context())
	isAdmin := IsAdmin(claims)

	// Build a set of online node IDs to mark status
	onlineNodes := h.nodeMgr.GetAll(r.Context())
	onlineSet := make(map[string]bool, len(onlineNodes))
	for _, n := range onlineNodes {
		onlineSet[n.ID] = true
	}

	type persistedInfo struct {
		ID          string        `json:"id"`
		Name        string        `json:"name"`
		Online      bool          `json:"online"`
		OwnerUserID string        `json:"owner_user_id"`
		TunnelCount int           `json:"tunnel_count"`
		Tunnels     []core.Tunnel `json:"tunnels"`
	}

	items := make([]persistedInfo, 0, len(allPersisted))
	for _, n := range allPersisted {
		if !isAdmin && n.OwnerUserID != claims.UserID {
			continue
		}
		items = append(items, persistedInfo{
			ID:          n.ID,
			Name:        n.Name,
			Online:      onlineSet[n.ID],
			OwnerUserID: n.OwnerUserID,
			TunnelCount: len(n.Tunnels),
			Tunnels:     n.Tunnels,
		})
	}

	ResponseOK(w, map[string]any{"items": items, "total": len(items)})
}

// Restart handles POST /api/v1/nodes/{id}/restart — send restart command to a client node.
func (h *NodeHandler) Restart(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/nodes/")
	id = strings.TrimSuffix(id, "/restart")
	id = strings.TrimRight(id, "/")
	if id == "" {
		ResponseError(w, http.StatusBadRequest, 400, "Node ID required")
		return
	}

	// 归属检查
	if node, ok := h.nodeMgr.Get(r.Context(), id); ok {
		if !checkNodeOwnership(w, r, node) {
			return
		}
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB
	var req struct {
		Delay  int    `json:"delay_seconds"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid request body")
		return
	}

	if h.controlSrv == nil {
		ResponseError(w, http.StatusInternalServerError, 500, "Control server not configured")
		return
	}

	if err := h.controlSrv.RestartNode(r.Context(), id, req.Delay, req.Reason); err != nil {
		slog.Error("RestartNode failed", "node", id, "error", err)
		ResponseError(w, http.StatusInternalServerError, 500, "Restart failed: "+err.Error())
		return
	}
	ResponseOK(w, map[string]any{"status": "ok", "node_id": id, "delay_seconds": req.Delay})
}

// UpdateRateLimit handles PATCH /api/v1/nodes/{id}/rate-limit
func (h *NodeHandler) UpdateRateLimit(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/nodes/")
	id = strings.TrimSuffix(id, "/rate-limit")
	id = strings.TrimRight(id, "/")

	node, ok := h.nodeMgr.Get(r.Context(), id)
	if !ok {
		ResponseError(w, http.StatusNotFound, 404, "Node not found")
		return
	}
	if !checkNodeOwnership(w, r, node) {
		return
	}

	var req struct {
		MaxConns int `json:"max_conns"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid request body")
		return
	}

	var rl *core.NodeRateLimit
	if req.MaxConns > 0 {
		rl = &core.NodeRateLimit{MaxConns: req.MaxConns}
	}

	if err := h.tunnelSvc.UpdateNodeRateLimit(r.Context(), id, rl); err != nil {
		if err == core.ErrTunnelInvalid {
			ResponseError(w, http.StatusBadRequest, 400, err.Error())
			return
		}
		if err == core.ErrNodeNotFound {
			ResponseError(w, http.StatusNotFound, 404, "Node not found")
			return
		}
		ResponseError(w, http.StatusInternalServerError, 500, err.Error())
		return
	}

	if node, ok := h.nodeMgr.Get(r.Context(), id); ok {
		ResponseOK(w, node)
	} else {
		ResponseError(w, http.StatusNotFound, 404, "Node not found")
	}
}
