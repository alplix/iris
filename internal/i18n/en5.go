package i18n

func init() {
	reg("en", map[string]string{
		"set.lang": "Language", "set.appearance": "Appearance", "set.theme": "Color theme",
		"set.startLocal": "Start bundled Iris client", "set.localStarted": "Iris client started. It may take a few seconds before it accepts connections.",
		"set.localFailed": "Failed to start", "set.localTitle": "Local Iris client",
		"set.stopped": "Stopped", "set.running": "Running", "set.stopLocal": "Stop client",
		"set.localStopped": "Iris client stopped",
		"about.title":      "About", "about.desc": "A volunteer computing manager for your Iris fleet.",
		"about.built":   "Built with Go + Wails. Connects to Iris/BOINC-compatible clients via GUI RPC.",
		"about.license": "2026 Alperen Yavuz. MIT License.",
	})
}
