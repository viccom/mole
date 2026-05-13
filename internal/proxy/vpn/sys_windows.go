//go:build windows
// +build windows

package vpn

import (
	"log"
	"os"
	"syscall"
)

// getSysProcAttr 获取系统进程属性
// 在 Windows 上需要设置 CREATE_NEW_PROCESS_GROUP 来正确终止子进程
func getSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

// getLogOutputPath 获取日志输出路径
func getLogOutputPath(name string) string {
	return ""
}

// StopProcess 优雅停止进程（Windows 专用）
// 先尝试通过生成 Ctrl+C 事件优雅停止，如果失败则降级到强制终止
func StopProcess(process *os.Process) error {
	if process == nil {
		return nil
	}
	// Windows 上，由于 CREATE_NEW_PROCESS_GROUP 标志，
	// GenerateConsoleCtrlEvent 可能不会有效，因为该进程组没有控制台
	// 所以直接使用 Kill 方式，避免不必要的错误日志
	log.Printf("vpn-manager: killing Windows process (PID: %d)", process.Pid)
	return process.Kill()
}
