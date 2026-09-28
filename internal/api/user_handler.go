package api

import (
	"encoding/json"
	"errors"
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
	nodeMgr        core.NodeManager // 运行态节点管理器（同步归属）
	feishuBindings core.FeishuBindingRepo
	dingtalkBindings core.DingTalkBindingRepo
}

func NewUserHandler(userRepo core.UserRepo, rbac *auth.RBACEngine, bcryptCost int, nodeRepo core.NodeRepo, accessTokenRepo core.AccessTokenRepo, nodeMgr core.NodeManager) *UserHandler {
	return &UserHandler{userRepo: userRepo, rbac: rbac, bcryptCost: bcryptCost, nodeRepo: nodeRepo, accessTokenRepo: accessTokenRepo, nodeMgr: nodeMgr}
}

// SetBindingRepos 注入 SSO 绑定仓储（删除用户时级联解绑；nil 跳过）
func (h *UserHandler) SetBindingRepos(feishu core.FeishuBindingRepo, dingtalk core.DingTalkBindingRepo) {
	h.feishuBindings = feishu
	h.dingtalkBindings = dingtalk
}

// reservedUserIDs 特权身份 ID 与真实用户共用同一命名空间：中间件对这些
// UserID 有 RBAC 旁路（access_key）或归属语义（system），允许注册同名
// 账号等于把旁路权限发给普通人
var reservedUserIDs = map[string]bool{"access_key": true, "system": true}

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
	if reservedUserIDs[req.Username] {
		ResponseError(w, http.StatusConflict, 409, "Username is reserved")
		return
	}
	if err := auth.ValidatePasswordStrength(req.Password); err != nil {
		ResponseError(w, http.StatusUnprocessableEntity, 422, err.Error())
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
	// 授角能力等价于 users:admin（专用路由 POST /users/{id}/roles/{roleId} 即以此
	// 权限注册）：Create 内嵌的 RoleIDs 不能成为绕过它的后门（users:write → 管理员）
	if len(req.RoleIDs) > 0 {
		claims := auth.GetClaims(r.Context())
		if claims != nil && !IsAdmin(claims) {
			allowed, perr := h.rbac.CheckPermission(claims.UserID, "users", "admin")
			if perr != nil || !allowed {
				ResponseError(w, http.StatusForbidden, 403, "Forbidden: assigning roles requires users:admin permission")
				return
			}
		}
	}
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

	if req.Username != "" && req.Username != user.Username {
		// 改名必须有唯一性/保留字校验：usernames 索引是无条件覆盖语义，
		// 重名会让 GetByUsername 解析到本用户（他人登录被打断、身份映射劫持）
		if reservedUserIDs[req.Username] {
			ResponseError(w, http.StatusConflict, 409, "Username is reserved")
			return
		}
		if _, err := h.userRepo.GetByUsername(req.Username); err == nil {
			ResponseError(w, http.StatusConflict, 409, "Username already exists")
			return
		} else if !errors.Is(err, core.ErrUserNotFound) {
			ResponseError(w, http.StatusInternalServerError, 500, "Failed to check username")
			return
		}
		user.Username = req.Username
	}
	if req.Status != "" {
		user.Status = core.UserStatus(req.Status)
	}
	if req.Password != "" {
		if err := auth.ValidatePasswordStrength(req.Password); err != nil {
			ResponseError(w, http.StatusUnprocessableEntity, 422, err.Error())
			return
		}
		hash, err := auth.HashPassword(req.Password, h.bcryptCost)
		if err != nil {
			ResponseError(w, http.StatusInternalServerError, 500, "Failed to hash password")
			return
		}
		// SetPasswordHash 失败必须中止：静默继续会谎报改密成功而旧密码仍有效
		if err := h.userRepo.SetPasswordHash(id, hash); err != nil {
			ResponseError(w, http.StatusInternalServerError, 500, "Failed to set password")
			return
		}
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

	// 级联处理 SSO 绑定：不清理则用户名回收后，原飞书/钉钉身份的 SSO 回调
	// 会命中残留绑定、直接给同名的重建账号签发 JWT（账号接管）
	if h.feishuBindings != nil {
		if err := h.feishuBindings.DeleteByUserID(id); err == nil {
			slog.Info("Deleted feishu binding for deleted user", "userId", id)
		}
	}
	if h.dingtalkBindings != nil {
		if err := h.dingtalkBindings.DeleteByUserID(id); err == nil {
			slog.Info("Deleted dingtalk binding for deleted user", "userId", id)
		}
	}

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
					// 落库前清运行态字段：这些字段不属于持久化契约
					n.SysInfo = nil
					n.ClientStatuses = nil
					n.RTT = 0
					if updateErr := h.nodeRepo.Update(n); updateErr != nil {
						slog.Warn("Failed to reassign node owner", "nodeId", n.ID, "error", updateErr)
					} else {
						migrated++
					}
				}
			}
			if migrated > 0 {
				slog.Info("Reassigned nodes from deleted user to system (persisted)", "userId", id, "count", migrated)
			}
		}
	}

	// 同步更新在线运行态中的归属
	if h.nodeMgr != nil {
		runtimeNodes := h.nodeMgr.GetAll(r.Context())
		runtimeUpdated := 0
		for _, n := range runtimeNodes {
			if n.OwnerUserID == id {
				h.nodeMgr.Update(r.Context(), n.ID, func(rn *core.Node) {
					rn.OwnerUserID = "system"
				})
				runtimeUpdated++
			}
		}
		if runtimeUpdated > 0 {
			slog.Info("Reassigned online nodes from deleted user to system (runtime)", "userId", id, "count", runtimeUpdated)
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
	// SEC-08：重置密码按 users:admin 收口——路由仅挂 users:write，旧逻辑让
	// 任意 users:write 操作者可改任何人（含 admin）的密码完成提权。自助改密
	// 请走 /auth/changepass（校验旧密码），本接口不做自助
	claims := auth.GetClaims(r.Context())
	if claims == nil || h.rbac == nil {
		ResponseError(w, http.StatusForbidden, 403, "Forbidden: password reset requires users:admin permission")
		return
	}
	allowed, err := h.rbac.CheckPermission(claims.UserID, "users", "admin")
	if err != nil || !allowed {
		ResponseError(w, http.StatusForbidden, 403, "Forbidden: password reset requires users:admin permission")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid request body")
		return
	}
	// 与 Create 对称的强度校验（1 位密码重置成功 = 弱口令入口）
	if err := auth.ValidatePasswordStrength(req.Password); err != nil {
		ResponseError(w, http.StatusUnprocessableEntity, 422, err.Error())
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

