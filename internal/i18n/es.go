package i18n

func init() {
	reg("es", map[string]string{
		"nav.dash": "Panel", "nav.tasks": "Tareas", "nav.projects": "Proyectos",
		"nav.transfers": "Transferencias", "nav.messages": "Mensajes", "nav.hosts": "Servidores",
		"nav.settings": "Ajustes", "nav.stats": "Estadisticas",
		"st.running": "Ejecutando", "st.paused": "Suspendido", "st.queued": "En cola",
		"st.error": "Error", "st.downloading": "Descargando", "st.uploading": "Subiendo",
		"run.always": "siempre", "run.auto": "auto", "run.never": "nunca",
		"common.cancel": "Cancelar", "common.edit": "Editar", "common.del": "Eliminar",
		"common.close": "Cerrar",
	})
}
