//go:build darwin

package main

// macOS has no tray: getlantern/systray and Wails both define the Cocoa
// AppDelegate class and cannot be linked into one binary. Closing the window
// quits the app there.
const hasTray = false

func startTray()                    {}
func stopTray()                     {}
func trayWindowReady()              {}
func applyTrayLanguage(code string) {}
