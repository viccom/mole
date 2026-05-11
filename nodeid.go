package moleAgent_client

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"unicode"

	"github.com/shirou/gopsutil/v3/cpu"
)

const nodeIDLength = 8

// HardwareNodeID 基于 CPU + MAC + machine-id 生成确定性节点 ID（8 字符，首字符字母）
func HardwareNodeID() (string, error) {
	var parts []string

	info, err := cpu.Info()
	if err == nil && len(info) > 0 {
		parts = append(parts, info[0].VendorID, info[0].ModelName)
	}

	if mac := firstHardwareMAC(); mac != "" {
		parts = append(parts, mac)
	}

	if mid := readMachineID(); mid != "" {
		parts = append(parts, mid)
	}

	if len(parts) == 0 {
		return "", fmt.Errorf("no hardware info available")
	}

	hash := sha256.Sum256([]byte(strings.Join(parts, "|")))
	id := hex.EncodeToString(hash[:])[:8]
	if id[0] >= '0' && id[0] <= '9' {
		id = string(rune(id[0]-'0'+'a')) + id[1:]
	}
	return id, nil
}

// firstHardwareMAC 返回第一个非回环网卡的 MAC 地址（小写冒号分隔）
func firstHardwareMAC() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	// 按 name 排序保证确定性
	sort.Slice(interfaces, func(i, j int) bool {
		return interfaces[i].Name < interfaces[j].Name
	})
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 || len(iface.HardwareAddr) == 0 {
			continue
		}
		return iface.HardwareAddr.String()
	}
	return ""
}

// readMachineID 读取 Linux machine-id
func readMachineID() string {
	for _, path := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		if data, err := os.ReadFile(path); err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	return ""
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
