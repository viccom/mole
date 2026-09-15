package storage

import (
	"fmt"
	"errors"
	"encoding/json"

	"github.com/nalgeon/redka"

	"moleAgent_Serv/internal/core"
)

type roleRepo struct {
	db *redka.DB
}

// NewRoleRepo 创建角色仓库
func NewRoleRepo(db *redka.DB) core.RoleRepo {
	return &roleRepo{db: db}
}

func (r *roleRepo) Create(role *core.Role) error {
	data, err := json.Marshal(role)
	if err != nil {
		return err
	}
	_, err = r.db.Hash().Set("roles", role.ID, string(data))
	return err
}

func (r *roleRepo) GetByID(id string) (*core.Role, error) {
	val, err := r.db.Hash().Get("roles", id)
	if err != nil {
		if errors.Is(err, redka.ErrNotFound) {
			return nil, core.ErrRoleNotFound
		}
		return nil, fmt.Errorf("read role %s: %w", id, err)
	}
	var role core.Role
	if err := json.Unmarshal([]byte(val.String()), &role); err != nil {
		return nil, err
	}
	return &role, nil
}

func (r *roleRepo) GetAll() ([]*core.Role, error) {
	items, err := r.db.Hash().Items("roles")
	if err != nil {
		return nil, err
	}
	roles := make([]*core.Role, 0, len(items))
	for _, v := range items {
		var role core.Role
		if err := json.Unmarshal([]byte(v.String()), &role); err != nil {
			continue
		}
		roles = append(roles, &role)
	}
	return roles, nil
}

func (r *roleRepo) Update(role *core.Role) error {
	data, err := json.Marshal(role)
	if err != nil {
		return err
	}
	_, err = r.db.Hash().Set("roles", role.ID, string(data))
	return err
}

func (r *roleRepo) Delete(id string) error {
	_, err := r.db.Hash().Delete("roles", id)
	return err
}
