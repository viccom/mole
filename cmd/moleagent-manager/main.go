package main

import (
	_ "embed"
	"log"

	"moleAgent_client/internal/shellui"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed build/windows/icon.ico
var iconData []byte

func main() {
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
		Height: 800,
		AssetServer: &assetserver.Options{
			Assets: shellui.Assets(),
		},
		BackgroundColour: &options.RGBA{R: 255, G: 255, B: 255, A: 255},
		OnStartup:      app.startup,
		OnDomReady:    app.domReady,
		OnBeforeClose: app.beforeClose,
		OnShutdown:    app.shutdown,
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
