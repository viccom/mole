//go:build windows
// +build windows

package vpn

import (
	"log"
	"os"
	"syscall"
)

// Windows process creation flags
const (
	CREATE_NEW_PROCESS_GROUP = 0x00000200
	CREATE_NO_WINDOW         = 0x08000000
)

// getSysProcAttr 获取系统进程属性
// 在 Windows 上需要设置 CREATE_NEW_PROCESS_GROUP 和 CREATE_NO_WINDOW
func getSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		// 创建独立进程组，以便正确终止进程
		CreationFlags: CREATE_NEW_PROCESS_GROUP | CREATE_NO_WINDOW,
		// 隐藏窗口
		HideWindow: true,
	}
}

// getLogOutputPath 获取日志输出路径
func getLogOutputPath(name string) string {
	return ""
}

// StopProcess 优雅停止进程（Windows 专用）
// 在 Windows 上直接使用 Kill 方式
func StopProcess(process *os.Process) error {
	if process == nil {
		return nil
	}
	log.Printf("vpn-manager: killing Windows process (PID: %d)", process.Pid)
	return process.Kill()
}
