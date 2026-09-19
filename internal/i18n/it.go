package i18n

func init() {
	reg("it", map[string]string{
		"nav.dash": "Pannello", "nav.tasks": "Attivita", "nav.projects": "Progetti",
		"nav.transfers": "Trasferimenti", "nav.messages": "Messaggi", "nav.hosts": "Server",
		"nav.settings": "Impostazioni", "nav.stats": "Statistiche",
		"st.running": "In esecuzione", "st.paused": "Sospeso", "st.queued": "In coda",
		"st.error": "Errore", "st.downloading": "Scaricamento", "st.uploading": "Caricamento",
		"run.always": "sempre", "run.auto": "auto", "run.never": "mai",
		"common.cancel": "Annulla", "common.edit": "Modifica", "common.del": "Rimuovi",
		"common.close": "Chiudi",
	})
}
