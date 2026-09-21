//go:build !darwin

package main

import (
	_ "embed"
	"os"
	"runtime"
	"sync"

	"github.com/alplix/iris/internal/app"
	"github.com/alplix/iris/internal/i18n"
	"github.com/alplix/iris/internal/product"
	"github.com/getlantern/systray"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// hasTray tells the window it can be hidden to the tray instead of closed.
const hasTray = true

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

// The tray's Open/Hide entries only work once the window exists, and the tray
// and the window come up independently of each other.
var tray struct {
	sync.Mutex
	show, hide *systray.MenuItem
	refresh    *systray.MenuItem
	quit       *systray.MenuItem
	windowUp   bool
}

// startTray registers the tray on the calling (main) thread; the event loop
// is the one Wails runs.
func startTray() { systray.Register(onTrayReady, onTrayExit) }

func stopTray() { systray.Quit() }

// trayWindowReady is called once the window exists.
func trayWindowReady() {
	tray.Lock()
	tray.windowUp = true
	tray.Unlock()
	enableTrayItems()
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

func onTrayExit() {}
