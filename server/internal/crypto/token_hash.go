package crypto

import (
	"crypto/sha256"
	"encoding/hex"
)

// GenerateTokenHash 计算 token 明文的 sha256 hex（接入 Token 的存储哈希）。
// 纯哈希函数，下沉到 crypto 使 api 层无需依赖 storage 包
// （架构审查 🔴5：api→storage 跨层直连）。
func GenerateTokenHash(rawToken string) string {
	h := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(h[:])
}
