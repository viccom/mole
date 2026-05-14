package storage

import (
	"encoding/json"
	"fmt"

	"github.com/nalgeon/redka"

	"moleAgent_Serv/internal/core"
)

type dingtalkBindingRepo struct {
	db *redka.DB
}

func NewDingTalkBindingRepo(db *redka.DB) core.DingTalkBindingRepo {
	return &dingtalkBindingRepo{db: db}
}

func (r *dingtalkBindingRepo) Create(binding *core.DingTalkBinding) error {
	data, err := json.Marshal(binding)
	if err != nil {
		return fmt.Errorf("marshal dingtalk binding: %w", err)
	}
	if _, err := r.db.Hash().Set("dingtalk_bindings", binding.UnionID, string(data)); err != nil {
		return fmt.Errorf("save dingtalk binding: %w", err)
	}
	if _, err := r.db.Hash().Set("dingtalk_user_bindings", binding.UserID, binding.UnionID); err != nil {
		r.db.Hash().Delete("dingtalk_bindings", binding.UnionID)
		return fmt.Errorf("save dingtalk user index: %w", err)
	}
	return nil
}

func (r *dingtalkBindingRepo) GetByUnionID(unionID string) (*core.DingTalkBinding, error) {
	val, err := r.db.Hash().Get("dingtalk_bindings", unionID)
	if err != nil {
		return nil, core.ErrNotFound
	}
	if val.String() == "" {
		return nil, core.ErrNotFound
	}
	var binding core.DingTalkBinding
	if err := json.Unmarshal([]byte(val.String()), &binding); err != nil {
		return nil, err
	}
	return &binding, nil
}

func (r *dingtalkBindingRepo) GetByUserID(userID string) (*core.DingTalkBinding, error) {
	unionIDVal, err := r.db.Hash().Get("dingtalk_user_bindings", userID)
	if err != nil {
		return nil, core.ErrNotFound
	}
	unionID := unionIDVal.String()
	if unionID == "" {
		return nil, core.ErrNotFound
	}
	return r.GetByUnionID(unionID)
}

func (r *dingtalkBindingRepo) DeleteByUnionID(unionID string) error {
	binding, err := r.GetByUnionID(unionID)
	if err != nil {
		return err
	}
	if _, err := r.db.Hash().Delete("dingtalk_bindings", unionID); err != nil {
		return err
	}
	if _, err := r.db.Hash().Delete("dingtalk_user_bindings", binding.UserID); err != nil {
		return fmt.Errorf("delete dingtalk user index: %w", err)
	}
	return nil
}

func (r *dingtalkBindingRepo) DeleteByUserID(userID string) error {
	binding, err := r.GetByUserID(userID)
	if err != nil {
		return err
	}
	return r.DeleteByUnionID(binding.UnionID)
}
