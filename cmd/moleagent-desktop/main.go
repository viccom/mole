package main

import (
	_ "embed"
	"log"
	"os"

	"moleAgent_client/internal/shellui"

	"moleAgent_client"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed build/windows/icon.ico
var iconData []byte

func main() {
	// MOLE_VERBOSE=1 开启 DEBUG 日志（webssh 会话细节等）。
	// GUI 无命令行入口，用环境变量；CLI 侧对应 -verbose flag
	if os.Getenv("MOLE_VERBOSE") != "" {
		moleAgent_client.EnableDebugLogging()
	}

	app := NewApp()

	err := wails.Run(
		buildOptions(app),
	)

	if err != nil {
		log.Fatal(err)
	}
}

func buildOptions(app *App) *options.App {
	return &options.App{
		Title:  "moleAgent Desktop",
		Width:  1280,
		Height: 960,
		AssetServer: &assetserver.Options{
			Assets: shellui.Assets(),
		},
		BackgroundColour: &options.RGBA{R: 255, G: 255, B: 255, A: 255},
		OnStartup:        app.startup,
		OnDomReady:       app.domReady,
		OnBeforeClose:    app.beforeClose,
		OnShutdown:       app.shutdown,
		// 单实例支持
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "moleAgent-desktop.wails.single-instance",
			OnSecondInstanceLaunch: func(data options.SecondInstanceData) {
				app.RequestActivate()
			},
		},
		Bind: []interface{}{
			app,
		},
	}
}
