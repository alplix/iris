package i18n

func init() {
	reg("de", map[string]string{
		"nav.dash": "Dashboard", "nav.tasks": "Aufgaben", "nav.projects": "Projekte",
		"nav.transfers": "Transfers", "nav.messages": "Nachrichten", "nav.hosts": "Server",
		"nav.settings": "Einstellungen", "nav.stats": "Statistiken",
		"st.running": "Laufend", "st.paused": "Angehalten", "st.queued": "Warteschlange",
		"st.error": "Fehler", "st.downloading": "Herunterladen", "st.uploading": "Hochladen",
		"run.always": "immer", "run.auto": "auto", "run.never": "nie",
		"common.cancel": "Abbrechen", "common.edit": "Bearbeiten", "common.del": "Entfernen",
		"common.close": "Schliessen",
	})
}
