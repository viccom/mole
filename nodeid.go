package moleAgent_client

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"unicode"

	"github.com/shirou/gopsutil/v3/cpu"
)

const nodeIDLength = 8

// HardwareNodeID 基于CPU信息生成确定性节点ID（8字符，首字符字母）
// 参考 apiAgent/wClient GetHardwareID 的设计：CPU VendorID + ModelName → SHA256
func HardwareNodeID() (string, error) {
	info, err := cpu.Info()
	if err != nil || len(info) == 0 {
		return "", fmt.Errorf("cpu info unavailable: %v", err)
	}
	hash := sha256.Sum256([]byte(info[0].VendorID + info[0].ModelName))
	id := hex.EncodeToString(hash[:])[:8]
	// 确保首字符是字母：hex 的 a-f 保持不变，0-9 映射到 a-j
	if id[0] >= '0' && id[0] <= '9' {
		id = string(rune(id[0]-'0'+'a')) + id[1:]
	}
	return id, nil
}

// DefaultNodeID 生成默认节点ID：优先硬件ID，fallback随机ID
func DefaultNodeID() string {
	if id, err := HardwareNodeID(); err == nil && ValidateNodeID(id) {
		return id
	}
	return GenerateNodeID()
}

// GenerateNodeID 生成随机 8 字符节点 ID（首字符字母，其余字母或数字）
// 作为硬件 ID 不可用时的 fallback
func GenerateNodeID() string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	const alphanum = "abcdefghijklmnopqrstuvwxyz0123456789"

	seed := make([]byte, nodeIDLength)
	if _, err := rand.Read(seed); err != nil {
		panic(fmt.Sprintf("crypto/rand.Read failed: %v", err))
	}

	id := make([]byte, nodeIDLength)
	id[0] = letters[seed[0]%byte(len(letters))]
	for i := 1; i < nodeIDLength; i++ {
		id[i] = alphanum[seed[i]%byte(len(alphanum))]
	}
	return string(id)
}

// ValidateNodeID 校验节点 ID 格式：固定 8 字符，首字符字母，其余字母或数字
func ValidateNodeID(id string) bool {
	if len(id) != nodeIDLength {
		return false
	}
	for i, r := range id {
		if i == 0 {
			if !unicode.IsLetter(r) {
				return false
			}
		} else {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
				return false
			}
		}
	}
	return true
}
