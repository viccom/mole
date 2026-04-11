package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"moleAgent_Serv/internal/core"
)

type RoleHandler struct {
	roleRepo core.RoleRepo
}

func NewRoleHandler(roleRepo core.RoleRepo) *RoleHandler {
	return &RoleHandler{roleRepo: roleRepo}
}

func (h *RoleHandler) List(w http.ResponseWriter, r *http.Request) {
	roles, err := h.roleRepo.GetAll()
	if err != nil {
		ResponseError(w, http.StatusInternalServerError, 500, "Failed to get roles")
		return
	}
	ResponseOK(w, roles)
}

func (h *RoleHandler) Create(w http.ResponseWriter, r *http.Request) {
	var role core.Role
	if err := json.NewDecoder(r.Body).Decode(&role); err != nil {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid request body")
		return
	}
	if role.Name == "" {
		ResponseError(w, http.StatusBadRequest, 400, "Role name required")
		return
	}
	if role.ID == "" {
		role.ID = role.Name
	}

	if err := h.roleRepo.Create(&role); err != nil {
		ResponseError(w, http.StatusConflict, 409, "Role already exists")
		return
	}
	ResponseOK(w, role)
}

func (h *RoleHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/roles/")
	id = strings.TrimRight(id, "/")
	role, err := h.roleRepo.GetByID(id)
	if err != nil {
		ResponseError(w, http.StatusNotFound, 404, "Role not found")
		return
	}
	ResponseOK(w, role)
}

func (h *RoleHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/roles/")
	id = strings.TrimRight(id, "/")
	role, err := h.roleRepo.GetByID(id)
	if err != nil {
		ResponseError(w, http.StatusNotFound, 404, "Role not found")
		return
	}
	var req core.Role
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid request body")
		return
	}
	if req.Name != "" {
		role.Name = req.Name
	}
	if req.Description != "" {
		role.Description = req.Description
	}
	if req.Permissions != nil {
		role.Permissions = req.Permissions
	}
	h.roleRepo.Update(role)
	ResponseOK(w, role)
}

func (h *RoleHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/roles/")
	id = strings.TrimRight(id, "/")
	if id == "admin" {
		ResponseError(w, http.StatusForbidden, 403, "Cannot delete admin role")
		return
	}
	h.roleRepo.Delete(id)
	ResponseOK(w, "deleted")
}

func (h *RoleHandler) ListPermissions(w http.ResponseWriter, r *http.Request) {
	permissions := []map[string]string{
		{"resource": "users", "action": "read"},
		{"resource": "users", "action": "write"},
		{"resource": "users", "action": "delete"},
		{"resource": "users", "action": "admin"},
		{"resource": "roles", "action": "read"},
		{"resource": "roles", "action": "write"},
		{"resource": "roles", "action": "delete"},
		{"resource": "nodes", "action": "read"},
		{"resource": "nodes", "action": "write"},
		{"resource": "nodes", "action": "delete"},
		{"resource": "nodes", "action": "admin"},
		{"resource": "tunnels", "action": "read"},
		{"resource": "tunnels", "action": "write"},
		{"resource": "tunnels", "action": "delete"},
		{"resource": "mqtt", "action": "read"},
		{"resource": "mqtt", "action": "write"},
		{"resource": "system", "action": "read"},
		{"resource": "system", "action": "admin"},
		{"resource": "accesskey", "action": "read"},
		{"resource": "accesskey", "action": "admin"},
		{"resource": "*", "action": "*"},
	}
	ResponseOK(w, permissions)
}
