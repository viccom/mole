package auth

import (
	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/crypto"
)

// MinPasswordLength 口令最小长度（口令策略唯一事实来源）
const MinPasswordLength = 8

// HashPassword 使用 bcrypt 生成密码哈希。
// 纯哈希实现已下沉 internal/crypto（架构审查 🔴5：消除 storage→auth 反向依赖），
// 此处保留导出名使既有调用方零改动。
func HashPassword(password string, cost int) (string, error) {
	return crypto.HashPassword(password, cost)
}

// VerifyPassword 验证密码是否匹配（实现同上下沉 crypto）
func VerifyPassword(password, hash string) bool {
	return crypto.VerifyPassword(password, hash)
}

// ValidatePasswordStrength 校验口令强度：所有落库口令的唯一入口
// （Create/Update/resetPassword/ChangePassword 共用，SEC-14——自助改密曾绕过）
func ValidatePasswordStrength(password string) error {
	if len(password) < MinPasswordLength {
		return core.ErrPasswordTooShort
	}
	return nil
}
