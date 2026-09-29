package auth

import (
	"encoding/json"
	"testing"

	"github.com/nalgeon/redka"
	_ "modernc.org/sqlite"

	"moleAgent_Serv/internal/core"
)

// TestRBACUserIDNotEqualUsername 测试当 userID != username 时，RBAC 权限检查仍正常工作
func TestRBACUserIDNotEqualUsername(t *testing.T) {
	// 使用内存 redka 数据库
	db, err := redka.Open(":memory:", &redka.Options{DriverName: "sqlite"})
	if err != nil {
		t.Fatalf("failed to open in-memory redka: %v", err)
	}
	defer db.Close()

	// 创建测试角色
	roleRepo := &mockRoleRepo{
		roles: map[string]*core.Role{
			"admin": {
				ID:   "admin",
				Name: "admin",
				Permissions: []core.Permission{
					{Resource: "*", Action: "*"},
				},
			},
			"operator": {
				ID:   "operator",
				Name: "operator",
				Permissions: []core.Permission{
					{Resource: "mqtt", Action: "read"},
				},
			},
		},
	}

	// 向内存 DB 写入用户角色映射（userID → roleIDs）
	// 关键：userID 是内部 UUID，username 是可显示的登录名，二者不同
	adminUserID := "uuid-admin-001"
	operatorUserID := "uuid-op-002"
	for _, uid := range []string{adminUserID, operatorUserID} {
		var roleIDsJSON string
		if uid == adminUserID {
			b, _ := json.Marshal([]string{"admin"})
			roleIDsJSON = string(b)
		} else if uid == operatorUserID {
			b, _ := json.Marshal([]string{"operator"})
			roleIDsJSON = string(b)
		}
		if _, err := db.Hash().Set("user_roles", uid, roleIDsJSON); err != nil {
			t.Fatalf("failed to set user_roles for %s: %v", uid, err)
		}
	}

	engine := NewRBACEngine(db, roleRepo)

	t.Run("userID与username不同时权限基于userID", func(t *testing.T) {
		// userID = "uuid-admin-001", username = "admin-user"
		// CheckPermission 应基于 userID，而非 username
		allowed, err := engine.CheckPermission("uuid-admin-001", "mqtt", "write")
		if err != nil {
			t.Fatalf("CheckPermission failed: %v", err)
		}
		if !allowed {
			t.Error("expected allowed=true for admin userID (has *:* permission)")
		}
	})

	t.Run("operator角色只能读mqtt", func(t *testing.T) {
		allowed, err := engine.CheckPermission("uuid-op-002", "mqtt", "write")
		if err != nil {
			t.Fatalf("CheckPermission failed: %v", err)
		}
		if allowed {
			t.Error("expected allowed=false for operator writing mqtt")
		}
	})

	t.Run("不存在的userID返回false", func(t *testing.T) {
		allowed, err := engine.CheckPermission("nonexistent-id", "mqtt", "read")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if allowed {
			t.Error("expected allowed=false for nonexistent userID")
		}
	})
}

// mockRoleRepo 用于测试的内存角色仓库
type mockRoleRepo struct {
	roles map[string]*core.Role
}

func (m *mockRoleRepo) Create(role *core.Role) error {
	m.roles[role.ID] = role
	return nil
}

func (m *mockRoleRepo) GetByID(id string) (*core.Role, error) {
	if r, ok := m.roles[id]; ok {
		return r, nil
	}
	return nil, core.ErrRoleNotFound
}

func (m *mockRoleRepo) GetAll() ([]*core.Role, error) {
	result := make([]*core.Role, 0, len(m.roles))
	for _, r := range m.roles {
		result = append(result, r)
	}
	return result, nil
}

func (m *mockRoleRepo) Update(role *core.Role) error {
	m.roles[role.ID] = role
	return nil
}

func (m *mockRoleRepo) Delete(id string) error {
	delete(m.roles, id)
	return nil
}
