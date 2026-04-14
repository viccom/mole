package storage

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/nalgeon/redka"
	_ "modernc.org/sqlite"

	"moleAgent_Serv/internal/auth"
	"moleAgent_Serv/internal/config"
	"moleAgent_Serv/internal/core"
)

var db *redka.DB

// Init 初始化数据库连接并创建种子数据
func Init(cfg config.DatabaseConfig) error {
	// 自动创建目录
	dir := filepath.Dir(cfg.Path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}

	opts := redka.Options{
		DriverName: "sqlite",
	}
	var err error
	db, err = redka.Open(cfg.Path, &opts)
	if err != nil {
		return err
	}

	slog.Info("Database opened", "path", cfg.Path)

	// 种子数据
	if err := seedData(); err != nil {
		return err
	}

	return nil
}

// Close 关闭数据库
func Close() {
	if db != nil {
		db.Close()
		slog.Info("Database closed")
	}
}

// DB 返回底层 redka.DB 实例
func DB() *redka.DB {
	return db
}

func seedData() error {
	// 1. 检查 admin 角色是否存在
	exists, err := db.Hash().Exists("roles", "admin")
	if err != nil {
		return err
	}
	if !exists {
		adminRole := core.Role{
			ID:          "admin",
			Name:        "admin",
			Description: "超级管理员",
			Permissions: []core.Permission{
				{Resource: "*", Action: "*"},
			},
		}
		data, err := json.Marshal(adminRole)
		if err != nil {
			return fmt.Errorf("marshal admin role: %w", err)
		}
		if _, err := db.Hash().Set("roles", "admin", string(data)); err != nil {
			return err
		}
		slog.Info("Seeded role: admin")
	}

	// 2. 检查 operator 角色
	exists, err = db.Hash().Exists("roles", "operator")
	if err != nil {
		return err
	}
	if !exists {
		operatorRole := core.Role{
			ID:          "operator",
			Name:        "operator",
			Description: "运维人员",
			Permissions: []core.Permission{
				{Resource: "users", Action: "read"},
				{Resource: "roles", Action: "read"},
				{Resource: "nodes", Action: "read"},
				{Resource: "tunnels", Action: "read"},
				{Resource: "mqtt", Action: "read"},
				{Resource: "system", Action: "read"},
			},
		}
		data, err := json.Marshal(operatorRole)
		if err != nil {
			return fmt.Errorf("marshal operator role: %w", err)
		}
		if _, err := db.Hash().Set("roles", "operator", string(data)); err != nil {
			return err
		}
		slog.Info("Seeded role: operator")
	}

	// 3. 检查 admin 用户
	exists, err = db.Hash().Exists("users", "admin")
	if err != nil {
		return err
	}
	if !exists {
		adminUser, adminPass := config.AdminUser()
		user := core.User{
			ID:        adminUser,
			Username:  adminUser,
			Status:    core.UserStatusActive,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
		userData, err := json.Marshal(user)
		if err != nil {
			return fmt.Errorf("marshal admin user: %w", err)
		}
		if _, err := db.Hash().Set("users", adminUser, string(userData)); err != nil {
			return err
		}
		slog.Info("Seeded user", "username", adminUser)

		if err := setPasswordHash(adminUser, adminPass); err != nil {
			return err
		}
		slog.Info("Seeded admin password set")

		if _, err := db.Hash().Set("user_roles", adminUser, `["admin"]`); err != nil {
			return err
		}
		slog.Info("Assigned admin role to admin user")

		// 维护 username → userID 索引
		db.Hash().Set("usernames", adminUser, adminUser)
	}

	// 迁移：为已有用户建立 username → userID 索引（幂等，仅补缺失的）
	items, err := db.Hash().Items("users")
	if err != nil {
		return fmt.Errorf("migrate usernames index: %w", err)
	}
	for key, val := range items {
		var user core.User
		if err := json.Unmarshal([]byte(val.String()), &user); err != nil {
			continue
		}
		if user.Username == "" {
			continue
		}
		exists, _ := db.Hash().Exists("usernames", user.Username)
		if !exists {
			if _, err := db.Hash().Set("usernames", user.Username, key); err != nil {
				slog.Warn("Failed to migrate username index", "username", user.Username, "error", err)
			} else {
				slog.Info("Migrated username index", "username", user.Username, "userID", key)
			}
		}
	}

	return nil
}

// setPasswordHash 使用 auth 包的 HashPassword 加密密码并存储
func setPasswordHash(userID, password string) error {
	hash, err := auth.HashPassword(password, 12)
	if err != nil {
		return err
	}
	_, err = db.Hash().Set("passwords", userID, hash)
	return err
}

// GetGlobalAccessKey 获取全局 AccessKey
func GetGlobalAccessKey() string {
	val, err := db.Str().Get("global_access_key")
	if err != nil {
		return ""
	}
	return val.String()
}
