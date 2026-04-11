package storage

import (
	"encoding/json"
	"time"

	"moleAgent_Serv/internal/core"
)

type userRepo struct{}

// NewUserRepo 创建用户仓库
func NewUserRepo() core.UserRepo {
	return &userRepo{}
}

func (r *userRepo) Create(user *core.User, passwordHash string) error {
	user.CreatedAt = time.Now().UTC()
	user.UpdatedAt = time.Now().UTC()
	if user.Status == "" {
		user.Status = core.UserStatusActive
	}

	data, err := json.Marshal(user)
	if err != nil {
		return err
	}
	if _, err := db.Hash().Set("users", user.ID, string(data)); err != nil {
		return err
	}
	if passwordHash != "" {
		if _, err := db.Hash().Set("passwords", user.ID, passwordHash); err != nil {
			return err
		}
	}
	return nil
}

func (r *userRepo) GetByID(id string) (*core.User, error) {
	val, err := db.Hash().Get("users", id)
	if err != nil {
		return nil, core.ErrUserNotFound
	}
	var user core.User
	if err := json.Unmarshal([]byte(val.String()), &user); err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *userRepo) GetByUsername(username string) (*core.User, error) {
	// 遍历所有用户（生产环境应有索引）
	items, err := db.Hash().Items("users")
	if err != nil {
		return nil, err
	}
	for _, v := range items {
		var user core.User
		if err := json.Unmarshal([]byte(v.String()), &user); err != nil {
			continue
		}
		if user.Username == username {
			return &user, nil
		}
	}
	return nil, core.ErrUserNotFound
}

func (r *userRepo) GetAll() ([]*core.User, error) {
	items, err := db.Hash().Items("users")
	if err != nil {
		return nil, err
	}
	users := make([]*core.User, 0, len(items))
	for _, v := range items {
		var user core.User
		if err := json.Unmarshal([]byte(v.String()), &user); err != nil {
			continue
		}
		users = append(users, &user)
	}
	return users, nil
}

func (r *userRepo) Update(user *core.User) error {
	user.UpdatedAt = time.Now().UTC()
	data, err := json.Marshal(user)
	if err != nil {
		return err
	}
	_, err = db.Hash().Set("users", user.ID, string(data))
	return err
}

func (r *userRepo) Delete(id string) error {
	if _, err := db.Hash().Delete("users", id); err != nil {
		return err
	}
	_, _ = db.Hash().Delete("passwords", id)
	_, _ = db.Hash().Delete("user_roles", id)
	return nil
}

func (r *userRepo) SetPasswordHash(id, hash string) error {
	if _, err := db.Hash().Set("passwords", id, hash); err != nil {
		return err
	}
	return nil
}

func (r *userRepo) GetPasswordHash(id string) (string, error) {
	val, err := db.Hash().Get("passwords", id)
	if err != nil {
		return "", core.ErrUserNotFound
	}
	return val.String(), nil
}
