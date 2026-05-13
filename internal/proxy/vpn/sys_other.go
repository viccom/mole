//go:build !windows
// +build !windows

package vpn

import (
	"os"
	"syscall"
)

// getSysProcAttr 获取系统进程属性
func getSysProcAttr() *syscall.SysProcAttr {
	return nil
}

// getLogOutputPath 获取日志输出路径
func getLogOutputPath(name string) string {
	return ""
}

// StopProcess 优雅停止进程（Unix/Linux/macOS 专用）
// 使用 SIGTERM 信号请求进程优雅退出
func StopProcess(process *os.Process) error {
	if process == nil {
		return nil
	}
	return process.Signal(syscall.SIGTERM)
}
