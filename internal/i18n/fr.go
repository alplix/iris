package i18n

func init() {
	reg("fr", map[string]string{
		"nav.dash": "Tableau de bord", "nav.tasks": "Taches", "nav.projects": "Projets",
		"nav.transfers": "Transferts", "nav.messages": "Messages", "nav.hosts": "Serveurs",
		"nav.settings": "Parametres", "nav.stats": "Statistiques",
		"st.running": "En cours", "st.paused": "Suspendu", "st.queued": "En file",
		"st.error": "Erreur", "st.downloading": "Telechargement", "st.uploading": "Envoi",
		"run.always": "toujours", "run.auto": "auto", "run.never": "jamais",
		"common.cancel": "Annuler", "common.edit": "Modifier", "common.del": "Supprimer",
		"common.close": "Fermer",
	})
}
