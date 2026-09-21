package main

import (
	"context"
	"embed"
	"os"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/alplix/iris/internal/app"
	"github.com/alplix/iris/internal/i18n"
	"github.com/alplix/iris/internal/product"
	"github.com/getlantern/systray"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/icon.png
var iconPNG []byte

// The Windows tray only accepts .ico data; the other platforms take PNG.
//
//go:embed build/icon.ico
var iconICO []byte

func trayIcon() []byte {
	if runtime.GOOS == "windows" {
		return iconICO
	}
	return iconPNG
}

var wailsApp *App

// quitting is set when the user picks Quit from the tray. Closing the window
// only hides it to the tray, so this tells OnBeforeClose to let the app exit.
var quitting atomic.Bool

// The tray's Open/Hide entries only work once the window exists, and the tray
// and the window come up independently of each other.
var tray struct {
	sync.Mutex
	show, hide *systray.MenuItem
	refresh    *systray.MenuItem
	quit       *systray.MenuItem
	windowUp   bool
}

// trayLabels returns the tray strings in the given language (English if the
// language is unknown or unset).
func trayLabels(code string) map[string]string {
	if !i18n.Has(code) {
		code = "en"
	}
	return i18n.Dump(code)
}

// applyTrayLanguage relabels the tray after the user changed the language.
func applyTrayLanguage(code string) {
	tray.Lock()
	defer tray.Unlock()
	if tray.show == nil {
		return
	}
	l := trayLabels(code)
	systray.SetTooltip(l["tray.tooltip"])
	tray.show.SetTitle(l["tray.open"])
	tray.show.SetTooltip(l["tray.openHint"])
	tray.refresh.SetTitle(l["tray.refresh"])
	tray.refresh.SetTooltip(l["tray.refreshHint"])
	tray.hide.SetTitle(l["tray.hide"])
	tray.hide.SetTooltip(l["tray.hideHint"])
	tray.quit.SetTitle(l["tray.quit"])
	tray.quit.SetTooltip(l["tray.quitHint"])
}

func enableTrayItems() {
	tray.Lock()
	defer tray.Unlock()
	if tray.windowUp && tray.show != nil {
		tray.show.Enable()
		tray.hide.Enable()
	}
}

func main() {
	// Wails and the tray share this thread: the Win32/GTK/Cocoa event loop
	// that Wails runs also delivers the tray's messages, and Wails needs the
	// thread to have COM initialised (WebView2).
	runtime.LockOSThread()
	if bindingsOnly {
		// `wails generate module` only runs the app to read its API: Run
		// writes the bindings and returns, so no tray is wanted.
		startWails()
		return
	}
	systray.Register(onTrayReady, onTrayExit)
	startWails()
}

func onTrayReady() {
	systray.SetIcon(trayIcon())
	systray.SetTitle(product.Name)
	l := trayLabels(app.LoadSettings().Lang)
	systray.SetTooltip(l["tray.tooltip"])

	mShow := systray.AddMenuItem(l["tray.open"], l["tray.openHint"])
	mRefresh := systray.AddMenuItem(l["tray.refresh"], l["tray.refreshHint"])
	mHide := systray.AddMenuItem(l["tray.hide"], l["tray.hideHint"])
	systray.AddSeparator()
	mQuit := systray.AddMenuItem(l["tray.quit"], l["tray.quitHint"])

	mShow.Disable()
	mHide.Disable()
	tray.Lock()
	tray.show, tray.hide, tray.refresh, tray.quit = mShow, mHide, mRefresh, mQuit
	tray.Unlock()
	enableTrayItems()

	go func() {
		for {
			select {
			case <-mShow.ClickedCh:
				if wailsApp != nil && wailsApp.ctx != nil {
					wailsApp.showWindow()
				}
			case <-mHide.ClickedCh:
				if wailsApp != nil && wailsApp.ctx != nil {
					wailsApp.hideWindow()
				}
			case <-mRefresh.ClickedCh:
				if wailsApp != nil && wailsApp.ctx != nil {
					wailsApp.RefreshAll()
				}
			case <-mQuit.ClickedCh:
				quitting.Store(true)
				if wailsApp != nil && wailsApp.ctx != nil {
					wailsruntime.Quit(wailsApp.ctx)
				} else {
					os.Exit(0)
				}
				return
			}
		}
	}()
}

func onTrayExit() {
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
			tray.Lock()
			tray.windowUp = true
			tray.Unlock()
			enableTrayItems()
		},
		OnShutdown: func(ctx context.Context) {
			wailsApp.shutdown(ctx)
			if !bindingsOnly {
				systray.Quit()
			}
		},
		// Returning true keeps the app running: closing the window only hides
		// it to the tray, unless the user chose Quit there.
		OnBeforeClose: func(ctx context.Context) bool {
			if quitting.Load() {
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
