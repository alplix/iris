package i18n

func init() {
	reg("tr", map[string]string{
		"set.lang": "Dil", "set.appearance": "Gorunum", "set.theme": "Tema rengi",
		"set.startLocal": "Paket Iris istemcisini baslat", "set.localStarted": "Iris istemcisi baslatildi. Baglanti kabul etmesi bir kac saniye surebilir.",
		"set.localFailed": "Baslatma basarisiz", "set.localTitle": "Yerel Iris istemcisi",
		"set.stopped": "Durdu", "set.running": "Calisiyor", "set.stopLocal": "Istemciyi durdur",
		"set.localStopped": "Iris istemcisi durduruldu",
		"about.title":      "Hakkinda", "about.desc": "Gonullu hesaplama filonuz icin Iris yoneticisi.",
		"about.built":   "Go + Wails ile yapildi. Iris uyumlu istemcilere GUI RPC ile baglanir.",
		"about.license": "2026 Alperen Yavuz. MIT Lisansi.",
	})
}
