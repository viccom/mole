//go:build windows

package main

import (
	"log"
	"runtime"
	"sync"

	"github.com/getlantern/systray"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// trayMenuManager manages tray icon and menu
type trayMenuManager struct {
	mu       sync.Mutex
	app      *App
	hidden   bool
	quitting bool
	ready    bool
}

var trayMgr *trayMenuManager

func initSystray(app *App) {
	trayMgr = &trayMenuManager{app: app}

	// 在新 goroutine 中运行 systray
	go func() {
		// 锁定到单个 OS 线程以确保稳定
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		systray.Run(trayOnReady, trayOnExit)
	}()
}

func trayOnReady() {
	log.Printf("tray: initializing")

	// 使用嵌入的图标数据
	iconData := getIconData()
	if len(iconData) == 0 {
		log.Printf("No embedded icon, using default")
		iconData = defaultTrayIcon()
	}
	systray.SetIcon(iconData)
	systray.SetTitle("moleAgent Manager")
	systray.SetTooltip("moleAgent Manager - 多实例管理")

	// 添加菜单
	showItem := systray.AddMenuItem("显示主界面", "显示主窗口")
	systray.AddSeparator()
	quitItem := systray.AddMenuItem("退出", "退出应用程序")

	// 设置托盘就绪
	trayMgr.mu.Lock()
	trayMgr.ready = true
	trayMgr.mu.Unlock()

	log.Printf("tray: ready")

	// 处理菜单点击 - 使用单独的 goroutine 处理每个菜单项
	go func() {
		for range showItem.ClickedCh {
			log.Printf("tray: show window clicked")
			showWindow()
		}
	}()

	go func() {
		for range quitItem.ClickedCh {
			log.Printf("tray: quit clicked")
			trayMgr.mu.Lock()
			trayMgr.quitting = true
			trayMgr.mu.Unlock()
			systray.Quit()
			if trayMgr.app != nil && trayMgr.app.ctx != nil {
				wailsRuntime.Quit(trayMgr.app.ctx)
			}
		}
	}()
}

func trayOnExit() {
	log.Printf("tray: exit")
	trayMgr.mu.Lock()
	trayMgr.ready = false
	trayMgr.mu.Unlock()
}

func showWindow() {
	log.Printf("tray: show window")
	if trayMgr != nil && trayMgr.app != nil && trayMgr.app.ctx != nil {
		wailsRuntime.WindowShow(trayMgr.app.ctx)
		wailsRuntime.WindowUnminimise(trayMgr.app.ctx)
		trayMgr.hidden = false
	}
}

// HideToTray 隐藏窗口到托盘
func HideToTray() {
	if trayMgr != nil && trayMgr.app != nil && trayMgr.app.ctx != nil {
		wailsRuntime.WindowHide(trayMgr.app.ctx)
		trayMgr.hidden = true
	}
}

// IsHidden 检查窗口是否已隐藏
func IsHidden() bool {
	return trayMgr != nil && trayMgr.hidden
}

// IsQuitting 检查是否通过托盘菜单退出
func IsQuitting() bool {
	if trayMgr == nil {
		return false
	}
	trayMgr.mu.Lock()
	defer trayMgr.mu.Unlock()
	return trayMgr.quitting
}

func defaultTrayIcon() []byte {
	// 16x16 蓝色图标数据（最小有效 ICO）
	return []byte{
		0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x10, 0x10, 0x00, 0x00, 0x01, 0x00, 0x20, 0x00,
		0x68, 0x04, 0x00, 0x00, 0x16, 0x00, 0x00, 0x00, 0x28, 0x00, 0x00, 0x00, 0x10, 0x00,
		0x00, 0x00, 0x20, 0x00, 0x00, 0x00, 0x01, 0x00, 0x20, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x04, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	}
}

func getIconData() []byte {
	return iconData
}
