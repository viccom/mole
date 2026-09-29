package version

import (
	"fmt"
	"os"
	"runtime"
)

var (
	Version   = "dev"
	BuildDate = "unknown"
	GitHash   = "unknown"
)

func VersionString() string {
	return fmt.Sprintf("%s (%s, %s)", Version, GitHash, BuildDate)
}

type SystemInfo struct {
	Version    string `json:"version"`
	GitHash    string `json:"git_hash"`
	BuildDate  string `json:"build_date"`
	BinaryPath string `json:"binary_path"`
	CPUNum     int    `json:"cpu_num"`
	Goroutines int    `json:"goroutines"`
	MemAllocMB uint64 `json:"mem_alloc_mb"`
	MemSysMB   uint64 `json:"mem_sys_mb"`
	P2P        bool   `json:"p2p"` // 构建能力标志：-tags p2p 构建=true（前端据此提示 P2P 不可用）
}

func GetSystemInfo() SystemInfo {
	exe, _ := os.Executable()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return SystemInfo{
		Version:    Version,
		GitHash:    GitHash,
		BuildDate:  BuildDate,
		BinaryPath: exe,
		CPUNum:     runtime.NumCPU(),
		Goroutines: runtime.NumGoroutine(),
		MemAllocMB: m.Alloc / 1024 / 1024,
		MemSysMB:   m.Sys / 1024 / 1024,
		P2P:        buildP2PEnabled,
	}
}
