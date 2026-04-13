package moleAgent_client

import (
	"crypto/rand"
	"fmt"
	"unicode"
)

const nodeIDLength = 8

// GenerateNodeID 生成随机 8 字符节点 ID（首字符字母，其余字母或数字）
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
