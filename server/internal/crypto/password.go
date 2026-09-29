package crypto

import "golang.org/x/crypto/bcrypt"

// 口令哈希纯函数层：bcrypt 只做「字符串进、布尔/哈希出」，无任何策略
// （最小长度、强度规则属 auth 域）。下沉到 crypto 使 storage 无需依赖 auth
// 包（架构审查 🔴5：底层仓储不应依赖中间层认证包）。

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
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
