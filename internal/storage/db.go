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

	// 迁移：为无归属节点设置 OwnerUserID = "system"
	if err := migrateNodeOwnership(); err != nil {
		slog.Warn("Failed to migrate node ownership", "error", err)
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
	adminUser, adminPass := config.AdminUser()
	if !exists {
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

		// SEC-05：随机生成的口令必须打印给运维，否则无人能登录
		if !config.AdminPassConfigured() {
			printAdminPassNotice(adminUser, adminPass)
		}
	}

	// 修复首启部分写入：种子三步（记录/密码/角色）非原子，中途崩溃后
	// Exists 只看 users 记录会跳过全部种子 → admin 有记录无密码，永久锁死
	if exists {
		if _, err := db.Hash().Get("passwords", adminUser); err != nil {
			if err := setPasswordHash(adminUser, adminPass); err != nil {
				return err
			}
			slog.Warn("Repaired admin password hash after partial seed", "username", adminUser)
			if !config.AdminPassConfigured() {
				printAdminPassNotice(adminUser, adminPass)
			}
		}
		if _, err := db.Hash().Get("user_roles", adminUser); err != nil {
			if _, err := db.Hash().Set("user_roles", adminUser, `["admin"]`); err != nil {
				return err
			}
			slog.Warn("Repaired admin role after partial seed", "username", adminUser)
		}
	}

	// SEC-05：库内哈希仍是公开默认口令 "admin" 时强制轮换——默认凭据在
	// 公开仓库可查，等于向所有能访问 API 的人开放管理员登录
	if err := rotateDefaultAdminPassword(adminUser, adminPass, config.AdminPassConfigured()); err != nil {
		return err
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

// defaultAdminPassword 公开仓库中的历史默认口令（SEC-05 轮换判据）
const defaultAdminPassword = "admin"

// rotateDefaultAdminPassword 检查 admin 用户库内哈希是否仍为公开默认口令，
// 是则用给定凭据重建：passFromEnv 为 true 时 password 来自 MA_ADMIN_PASS
// （无缝换密通道），否则为本次启动随机生成（打印一次性提示）
func rotateDefaultAdminPassword(username, password string, passFromEnv bool) error {
	hashVal, err := db.Hash().Get("passwords", username)
	if err != nil {
		return nil // 无哈希记录：由种子/修复路径负责
	}
	if !auth.VerifyPassword(defaultAdminPassword, hashVal.String()) {
		return nil // 已改密用户，尊重现状
	}
	if err := setPasswordHash(username, password); err != nil {
		return err
	}
	if passFromEnv {
		slog.Info("Rotated default admin password from MA_ADMIN_PASS", "username", username)
	} else {
		printAdminPassNotice(username, password)
	}
	return nil
}

// printAdminPassNotice 向 stderr 打印一次性随机口令提示（SEC-05）：
// 随机口令只在此处展示，首次登录后请立即改密或改用 MA_ADMIN_PASS
func printAdminPassNotice(username, password string) {
	fmt.Fprintf(os.Stderr, "[SECURITY] admin account %q is using a randomly generated password for this run: %s\n", username, password)
	fmt.Fprintf(os.Stderr, "[SECURITY] Log in with it and change the password immediately, or set MA_ADMIN_PASS before the next start to pin your own password.\n")
}

// GetGlobalAccessKey 获取全局 AccessKey
func GetGlobalAccessKey() string {
	val, err := db.Str().Get("global_access_key")
	if err != nil {
		return ""
	}
	return val.String()
}

// migrateNodeOwnership 将无归属节点的 OwnerUserID 设为 "system"
func migrateNodeOwnership() error {
	items, err := db.Hash().Items("nodes")
	if err != nil {
		return fmt.Errorf("migrate node ownership: %w", err)
	}
	migrated := 0
	for key, val := range items {
		var node core.Node
		if err := json.Unmarshal([]byte(val.String()), &node); err != nil {
			continue
		}
		if node.OwnerUserID == "" {
			node.OwnerUserID = "system"
			data, err := json.Marshal(node)
			if err != nil {
				continue
			}
			if _, err := db.Hash().Set("nodes", key, string(data)); err != nil {
				slog.Warn("Failed to migrate node ownership", "nodeId", key, "error", err)
				continue
			}
			migrated++
		}
	}
	if migrated > 0 {
		slog.Info("Migrated node ownership", "count", migrated, "ownerUserId", "system")
	}
	return nil
}
