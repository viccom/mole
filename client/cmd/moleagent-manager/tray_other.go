//go:build !windows

// tray_other.go 非 Windows 平台的托盘桩。
//
// tray.go 仅在 Windows 编译（systray 依赖），但跨平台的 app.go 引用
// initSystray/showWindow/HideToTray/IsQuitting 四个符号——此前缺桩导致
// 本模块在 Linux 上根本无法编译/vet（从未被根模块的 ./... 覆盖，故未被发现；
// make test/vet 纳入 GUI 子模块后暴露）。
//
// 非 Windows 行为降级：无托盘；showWindow/HideToTray 无操作（窗口由 WM
// 管理）；不处于托盘退出流程。
package main

import "log"

func initSystray(app *App) {
	log.Printf("tray: unsupported on this platform, running without tray")
}

func showWindow() {}

func HideToTray() {}

func IsQuitting() bool { return false }
