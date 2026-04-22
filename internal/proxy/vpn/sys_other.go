package vpn

import "syscall"

// getSysProcAttr 获取系统进程属性
func getSysProcAttr() *syscall.SysProcAttr {
	return nil
}

// getLogOutputPath 获取日志输出路径
func getLogOutputPath(name string) string {
	return ""
}
