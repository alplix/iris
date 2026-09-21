package main

import (
	"context"
	"embed"
	"os"
	"runtime"
	"sync/atomic"

	"github.com/alplix/iris/internal/product"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/icon.png
var iconPNG []byte

var wailsApp *App

// quitting is set when the user picks Quit from the tray. Closing the window
// only hides it to the tray, so this tells OnBeforeClose to let the app exit.
var quitting atomic.Bool

func main() {
	// Wails and the tray share this thread: the Win32/GTK event loop that
	// Wails runs also delivers the tray's messages, and Wails needs the
	// thread to have COM initialised (WebView2).
	runtime.LockOSThread()
	if !bindingsOnly {
		// `wails generate module` only runs the app to read its API and needs
		// no tray.
		startTray()
	}
	startWails()
}

func startWails() {
	wailsApp = NewApp()

	err := wails.Run(&options.App{
		Title:     product.Name,
		Width:     1280,
		Height:    800,
		MinWidth:  960,
		MinHeight: 640,
		BackgroundColour: &options.RGBA{
			R: 17, G: 16, B: 26, A: 255,
		},
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "dev.alplix.iris",
			OnSecondInstanceLaunch: func(_ options.SecondInstanceData) {
				if wailsApp != nil && wailsApp.ctx != nil {
					wailsApp.showWindow()
				}
			},
		},
		OnStartup: func(ctx context.Context) {
			wailsApp.startup(ctx)
			trayWindowReady()
		},
		OnShutdown: func(ctx context.Context) {
			wailsApp.shutdown(ctx)
			stopTray()
		},
		// Returning true keeps the app running: closing the window only hides
		// it to the tray (where there is one), unless the user chose Quit.
		OnBeforeClose: func(ctx context.Context) bool {
			if quitting.Load() || !hasTray {
				return false
			}
			if wailsApp != nil {
				wailsApp.hideWindow()
			}
			return true
		},
		Windows: &windows.Options{
			WebviewIsTransparent: true,
			WindowIsTranslucent:  true,
			BackdropType:         windows.Acrylic,
			Theme:                windows.SystemDefault,
		},
		Mac: &mac.Options{
			About: &mac.AboutInfo{
				Title:   product.Name + " " + product.Version,
				Message: "A volunteer computing grid manager.\n\n© 2026 Alperen Yavuz\n" + product.RepoURL,
				Icon:    iconPNG,
			},
		},
		Bind: []interface{}{
			wailsApp,
		},
	})
	if err != nil {
		println("Error:", err.Error())
		os.Exit(1)
	}
}
