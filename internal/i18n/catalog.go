package i18n

// Strings of the project picker in the "Add project" dialog.
func init() {
	reg("en", map[string]string{
		"cat.area": "Area", "cat.allAreas": "All areas", "cat.count": "{n} projects",
		"cat.none": "No project matches your search", "cat.runs": "Has apps for this server",
		"cat.noRun": "No app for this server", "cat.website": "Website", "cat.createAccount": "Create account",
		"cat.checking": "Checking the project...", "cat.reachable": "Project server reachable",
		"cat.unreachable":  "Cannot reach the project server ({e})",
		"cat.signupClosed": "New accounts cannot be created from the client; sign up on the website first.",
		"cat.platformOk":   "Has applications for {p}",
		"cat.platformNo":   "No applications for {p}: this server will probably get no work from this project.",
		"cat.custom":       "Or enter a project address yourself", "cat.pickHint": "Pick a project from the list.",
		"cat.community": "community-listed", "cat.selected": "Selected project",
	})
	reg("tr", map[string]string{
		"cat.area": "Alan", "cat.allAreas": "Tüm alanlar", "cat.count": "{n} proje",
		"cat.none": "Aramanıza uyan proje yok", "cat.runs": "Bu sunucu için uygulaması var",
		"cat.noRun": "Bu sunucu için uygulaması yok", "cat.website": "Web sitesi", "cat.createAccount": "Hesap oluştur",
		"cat.checking": "Proje denetleniyor...", "cat.reachable": "Proje sunucusuna ulaşılıyor",
		"cat.unreachable":  "Proje sunucusuna ulaşılamıyor ({e})",
		"cat.signupClosed": "İstemciden yeni hesap açılamıyor; önce web sitesinden kaydolun.",
		"cat.platformOk":   "{p} için uygulamaları var",
		"cat.platformNo":   "{p} için uygulaması yok: bu sunucuya büyük olasılıkla iş gönderilmez.",
		"cat.custom":       "Ya da proje adresini kendiniz girin", "cat.pickHint": "Listeden bir proje seçin.",
		"cat.community": "topluluk listesinde", "cat.selected": "Seçilen proje",
	})
	reg("de", map[string]string{
		"cat.area": "Bereich", "cat.allAreas": "Alle Bereiche", "cat.count": "{n} Projekte",
		"cat.none": "Kein Projekt passt zur Suche", "cat.runs": "Hat Apps für diesen Server",
		"cat.noRun": "Keine App für diesen Server", "cat.website": "Website", "cat.createAccount": "Konto erstellen",
		"cat.checking": "Projekt wird geprüft ...", "cat.reachable": "Projektserver erreichbar",
		"cat.unreachable":  "Projektserver nicht erreichbar ({e})",
		"cat.signupClosed": "Neue Konten lassen sich nicht über den Client anlegen; bitte zuerst auf der Website registrieren.",
		"cat.platformOk":   "Hat Anwendungen für {p}",
		"cat.platformNo":   "Keine Anwendungen für {p}: Dieser Server erhält von diesem Projekt wahrscheinlich keine Arbeit.",
		"cat.custom":       "Oder Projektadresse selbst eingeben", "cat.pickHint": "Wählen Sie ein Projekt aus der Liste.",
		"cat.community": "von der Community gelistet", "cat.selected": "Gewähltes Projekt",
	})
	reg("fr", map[string]string{
		"cat.area": "Domaine", "cat.allAreas": "Tous les domaines", "cat.count": "{n} projets",
		"cat.none": "Aucun projet ne correspond à la recherche", "cat.runs": "A des applications pour ce serveur",
		"cat.noRun": "Aucune application pour ce serveur", "cat.website": "Site web", "cat.createAccount": "Créer un compte",
		"cat.checking": "Vérification du projet...", "cat.reachable": "Serveur du projet joignable",
		"cat.unreachable":  "Serveur du projet injoignable ({e})",
		"cat.signupClosed": "Impossible de créer un compte depuis le client ; inscrivez-vous d'abord sur le site web.",
		"cat.platformOk":   "A des applications pour {p}",
		"cat.platformNo":   "Aucune application pour {p} : ce serveur ne recevra probablement pas de travail de ce projet.",
		"cat.custom":       "Ou saisissez vous-même l'adresse du projet", "cat.pickHint": "Choisissez un projet dans la liste.",
		"cat.community": "listé par la communauté", "cat.selected": "Projet sélectionné",
	})
	reg("es", map[string]string{
		"cat.area": "Área", "cat.allAreas": "Todas las áreas", "cat.count": "{n} proyectos",
		"cat.none": "Ningún proyecto coincide con la búsqueda", "cat.runs": "Tiene aplicaciones para este servidor",
		"cat.noRun": "Sin aplicación para este servidor", "cat.website": "Sitio web", "cat.createAccount": "Crear cuenta",
		"cat.checking": "Comprobando el proyecto...", "cat.reachable": "Servidor del proyecto accesible",
		"cat.unreachable":  "No se puede acceder al servidor del proyecto ({e})",
		"cat.signupClosed": "No se pueden crear cuentas nuevas desde el cliente; regístrate primero en el sitio web.",
		"cat.platformOk":   "Tiene aplicaciones para {p}",
		"cat.platformNo":   "Sin aplicaciones para {p}: este servidor probablemente no recibirá trabajo de este proyecto.",
		"cat.custom":       "O introduce tú mismo la dirección del proyecto", "cat.pickHint": "Elige un proyecto de la lista.",
		"cat.community": "listado por la comunidad", "cat.selected": "Proyecto seleccionado",
	})
	reg("it", map[string]string{
		"cat.area": "Area", "cat.allAreas": "Tutte le aree", "cat.count": "{n} progetti",
		"cat.none": "Nessun progetto corrisponde alla ricerca", "cat.runs": "Ha app per questo server",
		"cat.noRun": "Nessuna app per questo server", "cat.website": "Sito web", "cat.createAccount": "Crea account",
		"cat.checking": "Verifica del progetto...", "cat.reachable": "Server del progetto raggiungibile",
		"cat.unreachable":  "Server del progetto non raggiungibile ({e})",
		"cat.signupClosed": "Non è possibile creare nuovi account dal client; registrati prima sul sito web.",
		"cat.platformOk":   "Ha applicazioni per {p}",
		"cat.platformNo":   "Nessuna applicazione per {p}: questo server probabilmente non riceverà lavoro da questo progetto.",
		"cat.custom":       "Oppure inserisci tu l'indirizzo del progetto", "cat.pickHint": "Scegli un progetto dall'elenco.",
		"cat.community": "elencato dalla community", "cat.selected": "Progetto selezionato",
	})
	reg("pt", map[string]string{
		"cat.area": "Área", "cat.allAreas": "Todas as áreas", "cat.count": "{n} projetos",
		"cat.none": "Nenhum projeto corresponde à busca", "cat.runs": "Tem apps para este servidor",
		"cat.noRun": "Sem app para este servidor", "cat.website": "Site", "cat.createAccount": "Criar conta",
		"cat.checking": "Verificando o projeto...", "cat.reachable": "Servidor do projeto acessível",
		"cat.unreachable":  "Não foi possível acessar o servidor do projeto ({e})",
		"cat.signupClosed": "Não é possível criar contas novas pelo cliente; cadastre-se primeiro no site.",
		"cat.platformOk":   "Tem aplicativos para {p}",
		"cat.platformNo":   "Sem aplicativos para {p}: este servidor provavelmente não receberá trabalho deste projeto.",
		"cat.custom":       "Ou informe você mesmo o endereço do projeto", "cat.pickHint": "Escolha um projeto da lista.",
		"cat.community": "listado pela comunidade", "cat.selected": "Projeto selecionado",
	})
	reg("ru", map[string]string{
		"cat.area": "Область", "cat.allAreas": "Все области", "cat.count": "Проектов: {n}",
		"cat.none": "Нет проектов, подходящих под запрос", "cat.runs": "Есть приложения для этого сервера",
		"cat.noRun": "Нет приложения для этого сервера", "cat.website": "Сайт", "cat.createAccount": "Создать аккаунт",
		"cat.checking": "Проверка проекта...", "cat.reachable": "Сервер проекта доступен",
		"cat.unreachable":  "Сервер проекта недоступен ({e})",
		"cat.signupClosed": "Новые аккаунты нельзя создать из клиента; сначала зарегистрируйтесь на сайте.",
		"cat.platformOk":   "Есть приложения для {p}",
		"cat.platformNo":   "Нет приложений для {p}: этот сервер, скорее всего, не получит заданий от проекта.",
		"cat.custom":       "Или введите адрес проекта сами", "cat.pickHint": "Выберите проект из списка.",
		"cat.community": "добавлен сообществом", "cat.selected": "Выбранный проект",
	})
	reg("ja", map[string]string{
		"cat.area": "分野", "cat.allAreas": "すべての分野", "cat.count": "{n} 件のプロジェクト",
		"cat.none": "検索に一致するプロジェクトがありません", "cat.runs": "このサーバー向けのアプリあり",
		"cat.noRun": "このサーバー向けのアプリなし", "cat.website": "ウェブサイト", "cat.createAccount": "アカウントを作成",
		"cat.checking": "プロジェクトを確認中...", "cat.reachable": "プロジェクトサーバーに接続できます",
		"cat.unreachable":  "プロジェクトサーバーに接続できません ({e})",
		"cat.signupClosed": "クライアントから新規アカウントを作成できません。先にウェブサイトで登録してください。",
		"cat.platformOk":   "{p} 向けのアプリがあります",
		"cat.platformNo":   "{p} 向けのアプリがありません。このサーバーには作業が割り当てられない可能性があります。",
		"cat.custom":       "またはプロジェクトのアドレスを直接入力", "cat.pickHint": "リストからプロジェクトを選択してください。",
		"cat.community": "コミュニティ登録", "cat.selected": "選択中のプロジェクト",
	})
}
