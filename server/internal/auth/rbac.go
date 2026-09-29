package auth

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/nalgeon/redka"

	"moleAgent_Serv/internal/core"
)

type RBACEngine struct {
	db       *redka.DB
	roleRepo core.RoleRepo
}

func NewRBACEngine(db *redka.DB, roleRepo core.RoleRepo) *RBACEngine {
	return &RBACEngine{db: db, roleRepo: roleRepo}
}

// CheckPermission 检查用户是否拥有指定权限
func (re *RBACEngine) CheckPermission(userID, resource, action string) (bool, error) {
	roles, err := re.GetUserRoles(userID)
	if err != nil {
		return false, err
	}

	for _, role := range roles {
		for _, perm := range role.Permissions {
			if (perm.Resource == "*" || perm.Resource == resource) &&
				(perm.Action == "*" || perm.Action == action) {
				return true, nil
			}
		}
	}

	return false, nil
}

// GetUserRoles 获取用户的所有角色
func (re *RBACEngine) GetUserRoles(userID string) ([]core.Role, error) {
	val, err := re.db.Hash().Get("user_roles", userID)
	if err != nil {
		return nil, nil // 没有分配角色
	}

	var roleIDs []string
	if err := json.Unmarshal([]byte(val.String()), &roleIDs); err != nil {
		return nil, err
	}

	var roles []core.Role
	for _, id := range roleIDs {
		role, err := re.roleRepo.GetByID(id)
		if err != nil {
			slog.Warn("Role not found", "roleId", id, "userId", userID)
			continue
		}
		roles = append(roles, *role)
	}
	return roles, nil
}

// AssignRole 为用户分配角色
func (re *RBACEngine) AssignRole(userID, roleID string) error {
	// 检查角色是否存在
	_, err := re.roleRepo.GetByID(roleID)
	if err != nil {
		return core.ErrRoleNotFound
	}

	val, err := re.db.Hash().Get("user_roles", userID)
	var roleIDs []string
	if err == nil && val.String() != "" {
		if err := json.Unmarshal([]byte(val.String()), &roleIDs); err != nil {
			return fmt.Errorf("parse user roles for assign: %w", err)
		}
	}

	// 检查是否已分配
	for _, id := range roleIDs {
		if id == roleID {
			return nil
		}
	}

	roleIDs = append(roleIDs, roleID)
	data, err := json.Marshal(roleIDs)
	if err != nil {
		return fmt.Errorf("marshal role IDs: %w", err)
	}
	_, err = re.db.Hash().Set("user_roles", userID, string(data))
	return err
}

// RevokeRole 移除用户角色
func (re *RBACEngine) RevokeRole(userID, roleID string) error {
	val, err := re.db.Hash().Get("user_roles", userID)
	if err != nil {
		return nil // 没有角色
	}

	var roleIDs []string
	if err := json.Unmarshal([]byte(val.String()), &roleIDs); err != nil {
		return err
	}

	var newIDs []string
	for _, id := range roleIDs {
		if id != roleID {
			newIDs = append(newIDs, id)
		}
	}

	data, err := json.Marshal(newIDs)
	if err != nil {
		return fmt.Errorf("marshal role IDs for revoke: %w", err)
	}
	_, err = re.db.Hash().Set("user_roles", userID, string(data))
	return err
}
