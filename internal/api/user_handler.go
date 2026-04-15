package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"moleAgent_Serv/internal/auth"
	"moleAgent_Serv/internal/core"
)

type UserHandler struct {
	userRepo       core.UserRepo
	rbac           *auth.RBACEngine
	bcryptCost     int
	nodeRepo       core.NodeRepo
	accessTokenRepo core.AccessTokenRepo
}

func NewUserHandler(userRepo core.UserRepo, rbac *auth.RBACEngine, bcryptCost int, nodeRepo core.NodeRepo, accessTokenRepo core.AccessTokenRepo) *UserHandler {
	return &UserHandler{userRepo: userRepo, rbac: rbac, bcryptCost: bcryptCost, nodeRepo: nodeRepo, accessTokenRepo: accessTokenRepo}
}

func (h *UserHandler) List(w http.ResponseWriter, r *http.Request) {
	users, err := h.userRepo.GetAll()
	if err != nil {
		ResponseError(w, http.StatusInternalServerError, 500, "Failed to get users")
		return
	}
	ResponseOK(w, users)
}

func (h *UserHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string   `json:"username"`
		Password string   `json:"password"`
		Status   string   `json:"status"`
		RoleIDs  []string `json:"role_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid request body")
		return
	}
	if req.Username == "" || req.Password == "" {
		ResponseError(w, http.StatusBadRequest, 400, "Username and password required")
		return
	}
	if len(req.Password) < 8 {
		ResponseError(w, http.StatusUnprocessableEntity, 422, "Password must be at least 8 characters")
		return
	}

	// 检查是否已存在
	_, err := h.userRepo.GetByUsername(req.Username)
	if err == nil {
		ResponseError(w, http.StatusConflict, 409, "Username already exists")
		return
	}

	hash, err := auth.HashPassword(req.Password, h.bcryptCost)
	if err != nil {
		ResponseError(w, http.StatusInternalServerError, 500, "Failed to hash password")
		return
	}

	status := req.Status
	if status == "" {
		status = string(core.UserStatusActive)
	}

	user := &core.User{
		ID:       req.Username,
		Username: req.Username,
		Status:   core.UserStatus(status),
	}
	if err := h.userRepo.Create(user, hash); err != nil {
		ResponseError(w, http.StatusInternalServerError, 500, "Failed to create user")
		return
	}

	// 分配角色
	for _, roleID := range req.RoleIDs {
		if err := h.rbac.AssignRole(user.ID, roleID); err != nil {
			slog.Warn("Failed to assign role", "userId", user.ID, "roleId", roleID, "error", err)
		}
	}

	ResponseOK(w, user)
}

// Get 处理 GET /api/v1/users/{id} 和 GET /api/v1/users/{id}/roles
func (h *UserHandler) Get(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/users/")

	// 子路径处理
	if strings.HasSuffix(path, "/roles") {
		userID := strings.TrimSuffix(path, "/roles")
		h.listRoles(w, r, userID)
		return
	}

	id := strings.TrimRight(path, "/")
	user, err := h.userRepo.GetByID(id)
	if err != nil {
		ResponseError(w, http.StatusNotFound, 404, "User not found")
		return
	}
	ResponseOK(w, user)
}

// Update 处理 PUT /api/v1/users/{id}, PUT /users/{id}/status, PUT /users/{id}/password
func (h *UserHandler) Update(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/users/")

	// 子路径处理
	if strings.HasSuffix(path, "/status") {
		userID := strings.TrimSuffix(path, "/status")
		h.setStatus(w, r, userID)
		return
	}
	if strings.HasSuffix(path, "/password") {
		userID := strings.TrimSuffix(path, "/password")
		h.resetPassword(w, r, userID)
		return
	}

	id := strings.TrimRight(path, "/")
	user, err := h.userRepo.GetByID(id)
	if err != nil {
		ResponseError(w, http.StatusNotFound, 404, "User not found")
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Status   string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid request body")
		return
	}

	if req.Username != "" {
		user.Username = req.Username
	}
	if req.Status != "" {
		user.Status = core.UserStatus(req.Status)
	}
	if req.Password != "" {
		hash, err := auth.HashPassword(req.Password, h.bcryptCost)
		if err != nil {
			ResponseError(w, http.StatusInternalServerError, 500, "Failed to hash password")
			return
		}
		h.userRepo.SetPasswordHash(id, hash)
	}

	if err := h.userRepo.Update(user); err != nil {
		ResponseError(w, http.StatusInternalServerError, 500, "Failed to update user")
		return
	}
	ResponseOK(w, user)
}

// Delete 处理 DELETE /api/v1/users/{id} 和 DELETE /users/{id}/roles/{roleId}
func (h *UserHandler) Delete(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/users/")

	// 子路径处理: DELETE /users/{id}/roles/{roleId}
	if strings.Contains(path, "/roles/") {
		parts := strings.Split(path, "/roles/")
		if len(parts) == 2 {
			h.revokeRole(w, r, parts[0], parts[1])
			return
		}
	}

	id := strings.TrimRight(path, "/")

	// 级联处理：禁用该用户的 AccessToken，节点归属改为 system
	if h.accessTokenRepo != nil {
		tokens, err := h.accessTokenRepo.ListByUser(id)
		if err == nil {
			for _, t := range tokens {
				t.Status = core.AccessTokenDisabled
				if updateErr := h.accessTokenRepo.Update(t); updateErr != nil {
					slog.Warn("Failed to disable access token on user delete", "tokenId", t.ID, "error", updateErr)
				}
			}
			if len(tokens) > 0 {
				slog.Info("Disabled access tokens for deleted user", "userId", id, "count", len(tokens))
			}
		}
	}
	if h.nodeRepo != nil {
		nodes, err := h.nodeRepo.GetAll()
		if err == nil {
			migrated := 0
			for _, n := range nodes {
				if n.OwnerUserID == id {
					n.OwnerUserID = "system"
					if updateErr := h.nodeRepo.Update(n); updateErr != nil {
						slog.Warn("Failed to reassign node owner", "nodeId", n.ID, "error", updateErr)
					} else {
						migrated++
					}
				}
			}
			if migrated > 0 {
				slog.Info("Reassigned nodes from deleted user to system", "userId", id, "count", migrated)
			}
		}
	}

	if err := h.userRepo.Delete(id); err != nil {
		ResponseError(w, http.StatusInternalServerError, 500, "Failed to delete user")
		return
	}
	ResponseOK(w, "deleted")
}

// AssignRole 处理 POST /api/v1/users/{id}/roles/{roleId}
func (h *UserHandler) AssignRole(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/users/")
	parts := strings.Split(path, "/roles/")
	if len(parts) != 2 {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid path")
		return
	}
	userID := parts[0]
	roleID := parts[1]
	if err := h.rbac.AssignRole(userID, roleID); err != nil {
		ResponseError(w, http.StatusInternalServerError, 500, "Failed to assign role")
		return
	}
	ResponseOK(w, "role assigned")
}

// RevokeRole 处理 DELETE /api/v1/users/{id}/roles/{roleId}（备用入口）
func (h *UserHandler) RevokeRole(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/users/")
	parts := strings.Split(path, "/roles/")
	if len(parts) != 2 {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid path")
		return
	}
	h.revokeRole(w, r, parts[0], parts[1])
}

func (h *UserHandler) listRoles(w http.ResponseWriter, r *http.Request, userID string) {
	roles, err := h.rbac.GetUserRoles(userID)
	if err != nil {
		ResponseError(w, http.StatusInternalServerError, 500, "Failed to get roles")
		return
	}
	ResponseOK(w, roles)
}

func (h *UserHandler) setStatus(w http.ResponseWriter, r *http.Request, userID string) {
	user, err := h.userRepo.GetByID(userID)
	if err != nil {
		ResponseError(w, http.StatusNotFound, 404, "User not found")
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid request body")
		return
	}
	user.Status = core.UserStatus(req.Status)
	if err := h.userRepo.Update(user); err != nil {
			ResponseError(w, http.StatusInternalServerError, 500, "Failed to update user")
			return
		}
	ResponseOK(w, user)
}

func (h *UserHandler) resetPassword(w http.ResponseWriter, r *http.Request, userID string) {
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid request body")
		return
	}
	hash, err := auth.HashPassword(req.Password, h.bcryptCost)
	if err != nil {
		ResponseError(w, http.StatusInternalServerError, 500, "Failed to hash password")
		return
	}
	if err := h.userRepo.SetPasswordHash(userID, hash); err != nil {
		ResponseError(w, http.StatusInternalServerError, 500, "Failed to set password")
		return
	}
	ResponseOK(w, "password reset")
}

func (h *UserHandler) revokeRole(w http.ResponseWriter, r *http.Request, userID, roleID string) {
	if err := h.rbac.RevokeRole(userID, roleID); err != nil {
		ResponseError(w, http.StatusInternalServerError, 500, "Failed to revoke role")
		return
	}
	ResponseOK(w, "role revoked")
}

