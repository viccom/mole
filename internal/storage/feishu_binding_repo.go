package storage

import (
	"encoding/json"
	"fmt"

	"github.com/nalgeon/redka"

	"moleAgent_Serv/internal/core"
)

type feishuBindingRepo struct {
	db *redka.DB
}

func NewFeishuBindingRepo(db *redka.DB) core.FeishuBindingRepo {
	return &feishuBindingRepo{db: db}
}

func (r *feishuBindingRepo) Create(binding *core.FeishuBinding) error {
	data, err := json.Marshal(binding)
	if err != nil {
		return fmt.Errorf("marshal feishu binding: %w", err)
	}
	if _, err := r.db.Hash().Set("feishu_bindings", binding.OpenID, string(data)); err != nil {
		return fmt.Errorf("save feishu binding: %w", err)
	}
	if _, err := r.db.Hash().Set("feishu_user_bindings", binding.UserID, binding.OpenID); err != nil {
		r.db.Hash().Delete("feishu_bindings", binding.OpenID)
		return fmt.Errorf("save feishu user index: %w", err)
	}
	return nil
}

func (r *feishuBindingRepo) GetByOpenID(openID string) (*core.FeishuBinding, error) {
	val, err := r.db.Hash().Get("feishu_bindings", openID)
	if err != nil {
		return nil, core.ErrNotFound
	}
	if val.String() == "" {
		return nil, core.ErrNotFound
	}
	var binding core.FeishuBinding
	if err := json.Unmarshal([]byte(val.String()), &binding); err != nil {
		return nil, err
	}
	return &binding, nil
}

func (r *feishuBindingRepo) GetByUserID(userID string) (*core.FeishuBinding, error) {
	openIDVal, err := r.db.Hash().Get("feishu_user_bindings", userID)
	if err != nil {
		return nil, core.ErrNotFound
	}
	openID := openIDVal.String()
	if openID == "" {
		return nil, core.ErrNotFound
	}
	return r.GetByOpenID(openID)
}

func (r *feishuBindingRepo) DeleteByOpenID(openID string) error {
	binding, err := r.GetByOpenID(openID)
	if err != nil {
		return err
	}
	if _, err := r.db.Hash().Delete("feishu_bindings", openID); err != nil {
		return err
	}
	if _, err := r.db.Hash().Delete("feishu_user_bindings", binding.UserID); err != nil {
		return fmt.Errorf("delete feishu user index: %w", err)
	}
	return nil
}

func (r *feishuBindingRepo) DeleteByUserID(userID string) error {
	binding, err := r.GetByUserID(userID)
	if err != nil {
		return err
	}
	return r.DeleteByOpenID(binding.OpenID)
}
