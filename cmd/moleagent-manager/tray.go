//go:build windows

package main

import (
	"log"
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
}

var trayMgr *trayMenuManager

func initSystray(app *App) {
	trayMgr = &trayMenuManager{app: app}

	// 在新 goroutine 中运行 systray（它会阻塞）
	go func() {
		systray.Run(trayOnReady, trayOnExit)
	}()
}

func trayOnReady() {
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

	// 处理菜单点击
	go func() {
		for {
			select {
			case <-showItem.ClickedCh:
				showWindow()
			case <-quitItem.ClickedCh:
				trayMgr.mu.Lock()
				trayMgr.quitting = true
				trayMgr.mu.Unlock()
				systray.Quit()
				if trayMgr.app != nil && trayMgr.app.ctx != nil {
					wailsRuntime.Quit(trayMgr.app.ctx)
				}
			}
		}
	}()
}

func trayOnExit() {
	// 清理
}

func showWindow() {
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
