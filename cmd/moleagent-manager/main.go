package main

import (
	_ "embed"
	"log"
	"log/slog"
	"os"

	"moleAgent_client/internal/shellui"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed build/windows/icon.ico
var iconData []byte

func main() {
	// MOLE_VERBOSE=1 开启 DEBUG 日志。GUI 无命令行入口，用环境变量；
	// CLI 侧对应 -verbose flag。此处直接调 slog 而非引入根包
	// （manager 模块本不依赖 moleAgent_client，避免拖入整棵依赖树）
	if os.Getenv("MOLE_VERBOSE") != "" {
		slog.SetLogLoggerLevel(slog.LevelDebug)
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
		Title:  "moleAgent Manager",
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
			UniqueId: "moleAgent-manager.wails.single-instance",
			OnSecondInstanceLaunch: func(data options.SecondInstanceData) {
				app.RequestActivate()
			},
		},
		Bind: []interface{}{
			app,
		},
	}
}
