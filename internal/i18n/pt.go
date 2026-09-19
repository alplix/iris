package i18n

func init() {
	reg("pt", map[string]string{
		"nav.dash": "Painel", "nav.tasks": "Tarefas", "nav.projects": "Projetos",
		"nav.transfers": "Transferencias", "nav.messages": "Mensagens", "nav.hosts": "Servidores",
		"nav.settings": "Configuracoes", "nav.stats": "Estatisticas",
		"st.running": "Executando", "st.paused": "Suspenso", "st.queued": "Na fila",
		"st.error": "Erro", "st.downloading": "Baixando", "st.uploading": "Enviando",
		"run.always": "sempre", "run.auto": "auto", "run.never": "nunca",
		"common.cancel": "Cancelar", "common.edit": "Editar", "common.del": "Remover",
		"common.close": "Fechar",
	})
}
