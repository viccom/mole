package auth

import "golang.org/x/crypto/bcrypt"

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
