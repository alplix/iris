//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa
#include "tray_objc_darwin.h"
#include <stdlib.h>
*/
import "C"

import (
	"os"
	"unsafe"

	"github.com/alplix/iris/internal/app"
	"github.com/alplix/iris/internal/i18n"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// hasTray tells the window it can be hidden to the tray instead of closed.
//
// This is a real NSStatusItem-based tray, not getlantern/systray: that
// library and Wails both register their own Cocoa NSApplicationDelegate and
// cannot be linked into the same binary (crashes at startup). The
// implementation here (tray_objc_darwin.m) never touches NSApp's delegate
// at all — only Wails does — so there is nothing for the two to collide
// over. See https://github.com/wailsapp/wails/discussions/4514 for the
// community fix this follows.
const hasTray = true

// trayLabels returns the tray strings in the given language (English if the
// language is unknown or unset).
func trayLabels(code string) map[string]string {
	if !i18n.Has(code) {
		code = "en"
	}
	return i18n.Dump(code)
}

// cTrayStrings converts the label set the tray needs into owned C strings;
// the returned free func must be called once the C call that used them
// returns (Cocoa copies what it needs out of them synchronously, so they
// don't need to outlive the call).
func cTrayStrings(l map[string]string) (strs [9]*C.char, free func()) {
	keys := [9]string{
		"tray.tooltip",
		"tray.open", "tray.openHint",
		"tray.refresh", "tray.refreshHint",
		"tray.hide", "tray.hideHint",
		"tray.quit", "tray.quitHint",
	}
	for i, k := range keys {
		strs[i] = C.CString(l[k])
	}
	return strs, func() {
		for _, s := range strs {
			C.free(unsafe.Pointer(s))
		}
	}
}

// startTray creates the menu bar item on the calling (main) thread, same as
// the non-Darwin tray — it runs before wails.Run starts the real Cocoa event
// loop, which is fine: the status item just won't be drawn until that loop
// is pumping, exactly like Wails' own window.
func startTray() {
	l := trayLabels(app.LoadSettings().Lang)
	s, free := cTrayStrings(l)
	defer free()

	C.iris_tray_start(
		unsafe.Pointer(&iconPNG[0]), C.long(len(iconPNG)),
		s[0],
		s[1], s[2],
		s[3], s[4],
		s[5], s[6],
		s[7], s[8],
	)
}

func stopTray() { C.iris_tray_stop() }

// trayWindowReady is called once the window exists; Open/Hide only make
// sense once it does.
func trayWindowReady() { C.iris_tray_set_window_up(1) }

// applyTrayLanguage relabels the tray after the user changed the language.
func applyTrayLanguage(code string) {
	l := trayLabels(code)
	s, free := cTrayStrings(l)
	defer free()

	C.iris_tray_relabel(
		s[0],
		s[1], s[2],
		s[3], s[4],
		s[5], s[6],
		s[7], s[8],
	)
}

// goTrayAction is called from tray_objc_darwin.m's menu item handlers,
// still on the main thread. action: 0=show, 1=refresh, 2=hide, 3=quit.
//
//export goTrayAction
func goTrayAction(action C.int) {
	switch action {
	case 0:
		if wailsApp != nil && wailsApp.ctx != nil {
			wailsApp.showWindow()
		}
	case 1:
		if wailsApp != nil && wailsApp.ctx != nil {
			wailsApp.RefreshAll()
		}
	case 2:
		if wailsApp != nil && wailsApp.ctx != nil {
			wailsApp.hideWindow()
		}
	case 3:
		quitting.Store(true)
		if wailsApp != nil && wailsApp.ctx != nil {
			wailsruntime.Quit(wailsApp.ctx)
		} else {
			os.Exit(0)
		}
	}
}
