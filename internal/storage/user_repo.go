package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nalgeon/redka"

	"moleAgent_Serv/internal/core"
)

type userRepo struct {
	db *redka.DB
}

// NewUserRepo 创建用户仓库
func NewUserRepo(db *redka.DB) core.UserRepo {
	return &userRepo{db: db}
}

func (r *userRepo) Create(user *core.User, passwordHash string) error {
	// 存在性检查必须落在 repo 层：redka hash 无唯一约束，无条件 upsert 会把
	// 已存在账号（含密码哈希）静默整体覆盖；handler 预检可被并发创建穿透
	if _, err := r.GetByID(user.ID); err == nil {
		return core.ErrUserExists
	} else if !errors.Is(err, core.ErrUserNotFound) {
		return err
	}

	user.CreatedAt = time.Now().UTC()
	user.UpdatedAt = time.Now().UTC()
	if user.Status == "" {
		user.Status = core.UserStatusActive
	}

	data, err := json.Marshal(user)
	if err != nil {
		return err
	}
	if _, err := r.db.Hash().Set("users", user.ID, string(data)); err != nil {
		return err
	}
	if passwordHash != "" {
		if _, err := r.db.Hash().Set("passwords", user.ID, passwordHash); err != nil {
			return err
		}
	}
	// 维护 username → userID 索引
	if _, err := r.db.Hash().Set("usernames", user.Username, user.ID); err != nil {
		return err
	}
	return nil
}

func (r *userRepo) GetByID(id string) (*core.User, error) {
	val, err := r.db.Hash().Get("users", id)
	if err != nil {
		// 区分「不存在」与真 DB 错误：把超时/IO 故障折叠成 NotFound 会穿透
		// 上层的存在性预检（node_repo/p2p_token_repo 已修的同类问题）
		if errors.Is(err, redka.ErrNotFound) {
			return nil, core.ErrUserNotFound
		}
		return nil, fmt.Errorf("read user %s: %w", id, err)
	}
	var user core.User
	if err := json.Unmarshal([]byte(val.String()), &user); err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *userRepo) GetByUsername(username string) (*core.User, error) {
	// 通过 username → userID 索引查找
	userID, err := r.db.Hash().Get("usernames", username)
	if err != nil {
		if errors.Is(err, redka.ErrNotFound) {
			return nil, core.ErrUserNotFound
		}
		return nil, fmt.Errorf("read username index %s: %w", username, err)
	}
	if userID.String() == "" {
		return nil, core.ErrUserNotFound
	}
	return r.GetByID(userID.String())
}

func (r *userRepo) GetAll() ([]*core.User, error) {
	items, err := r.db.Hash().Items("users")
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
	existing, err := r.GetByID(user.ID)
	if err != nil {
		return err
	}

	user.UpdatedAt = time.Now().UTC()
	data, err := json.Marshal(user)
	if err != nil {
		return err
	}
	if _, err := r.db.Hash().Set("users", user.ID, string(data)); err != nil {
		return err
	}
	if existing.Username != "" && existing.Username != user.Username {
		if _, err := r.db.Hash().Delete("usernames", existing.Username); err != nil {
			return err
		}
	}
	// 维护 username 索引。先删除旧索引，再写入新索引，避免崩溃后旧用户名残留。
	if _, err := r.db.Hash().Set("usernames", user.Username, user.ID); err != nil {
		return err
	}
	return nil
}

func (r *userRepo) Delete(id string) error {
	// 先获取用户信息用于清理索引
	existing, _ := r.GetByID(id)
	if _, err := r.db.Hash().Delete("users", id); err != nil {
		return err
	}
	r.db.Hash().Delete("passwords", id)
	r.db.Hash().Delete("user_roles", id)
	if existing != nil {
		r.db.Hash().Delete("usernames", existing.Username)
	}
	return nil
}

func (r *userRepo) SetPasswordHash(id, hash string) error {
	if _, err := r.db.Hash().Set("passwords", id, hash); err != nil {
		return err
	}
	return nil
}

func (r *userRepo) GetPasswordHash(id string) (string, error) {
	val, err := r.db.Hash().Get("passwords", id)
	if err != nil {
		if errors.Is(err, redka.ErrNotFound) {
			return "", core.ErrUserNotFound
		}
		return "", fmt.Errorf("read password hash %s: %w", id, err)
	}
	return val.String(), nil
}
