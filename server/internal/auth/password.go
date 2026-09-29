package auth

import (
	"golang.org/x/crypto/bcrypt"

	"moleAgent_Serv/internal/core"
)

// MinPasswordLength 口令最小长度（口令策略唯一事实来源）
const MinPasswordLength = 8

// HashPassword 使用 bcrypt 生成密码哈希
func HashPassword(password string, cost int) (string, error) {
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", err
	}
	return string(hashed), nil
}

// VerifyPassword 验证密码是否匹配
func VerifyPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// ValidatePasswordStrength 校验口令强度：所有落库口令的唯一入口
// （Create/Update/resetPassword/ChangePassword 共用，SEC-14——自助改密曾绕过）
func ValidatePasswordStrength(password string) error {
	if len(password) < MinPasswordLength {
		return core.ErrPasswordTooShort
	}
	return nil
}
