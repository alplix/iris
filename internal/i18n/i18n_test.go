package i18n

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

// UTF-8 text mis-decoded as Windows-125x shows a lead character (Ã Â Ä â ð)
// followed by a symbol from the C1/Latin-1 punctuation range. Real words such
// as "tâche" or "Configurações" never have that shape.
var mojibakeRe = regexp.MustCompile("[ÂÃÄâð][-¿ŒœŠšŸŽžƒˆ˜–-›€™]")

var placeholderRe = regexp.MustCompile(`\{[a-zA-Z]+\}`)

func placeholders(s string) []string {
	ph := placeholderRe.FindAllString(s, -1)
	sort.Strings(ph)
	return ph
}

func TestEveryLanguageIsRegistered(t *testing.T) {
	for _, code := range Available() {
		if !Has(code) {
			t.Errorf("language %q is listed but has no strings", code)
		}
	}
	if len(Languages()) != len(Available()) {
		t.Error("Languages() must cover every available code")
	}
	for _, l := range Languages() {
		if l.Name == "" {
			t.Errorf("language %q has no display name", l.Code)
		}
	}
}

// Every language must translate every string, and only strings English has.
func TestEveryLanguageCoversEveryEnglishString(t *testing.T) {
	en := allLangs["en"].data
	for _, code := range Available() {
		if code == "en" {
			continue
		}
		tr := allLangs[code].data
		for k := range en {
			if _, ok := tr[k]; !ok {
				t.Errorf("%s is missing %q", code, k)
			}
		}
		for k := range tr {
			if _, ok := en[k]; !ok {
				t.Errorf("%s has %q, which English does not define", code, k)
			}
		}
	}
}

func TestTranslationsKeepPlaceholdersAndSurviveEncoding(t *testing.T) {
	en := allLangs["en"].data
	for _, code := range Available() {
		for k, v := range allLangs[code].data {
			base, ok := en[k]
			if !ok {
				t.Errorf("%s: key %q does not exist in English", code, k)
				continue
			}
			if strings.Join(placeholders(v), ",") != strings.Join(placeholders(base), ",") {
				t.Errorf("%s/%s: placeholders %v differ from English %v", code, k, placeholders(v), placeholders(base))
			}
			if !utf8.ValidString(v) || strings.ContainsRune(v, utf8.RuneError) {
				t.Errorf("%s/%s: invalid UTF-8", code, k)
			}
			// A value made of nothing but question marks means the text was
			// lost in an encoding round trip; Ã/Â/Ä/â lead bytes mean it was
			// double-encoded.
			if strings.Trim(v, "?") == "" && v != "" {
				t.Errorf("%s/%s: value is only question marks", code, k)
			}
			if mojibakeRe.MatchString(v) {
				t.Errorf("%s/%s: looks double-encoded: %q", code, k, v)
			}
		}
	}
}

func TestDumpIsCompleteAndFallsBackToEnglish(t *testing.T) {
	en := Dump("en")
	for _, code := range Available() {
		if len(Dump(code)) != len(en) {
			t.Errorf("%s dump has %d keys, want %d", code, len(Dump(code)), len(en))
		}
	}
	if Dump("de")["nav.tasks"] != "Aufgaben" {
		t.Errorf("de nav.tasks = %q", Dump("de")["nav.tasks"])
	}
	if got := Dump("xx"); got["nav.tasks"] != en["nav.tasks"] || len(got) != len(en) {
		t.Error("an unknown language should dump English")
	}
}

func TestTurkishUsesTurkishLetters(t *testing.T) {
	tr := Dump("tr")
	if tr["nav.tasks"] != "Görevler" || tr["ui.offline"] != "Çevrimdışı" {
		t.Errorf("tr must be written with ç ğ ı ö ş ü, got %q / %q", tr["nav.tasks"], tr["ui.offline"])
	}
}

func TestJapaneseAndRussianAreReal(t *testing.T) {
	if !strings.Contains(allLangs["ja"].data["nav.tasks"], "タスク") {
		t.Errorf("ja nav.tasks = %q", allLangs["ja"].data["nav.tasks"])
	}
	if allLangs["ru"].data["nav.tasks"] != "Задачи" {
		t.Errorf("ru nav.tasks = %q", allLangs["ru"].data["nav.tasks"])
	}
}

// Every T('key') call in the frontend must resolve to an English string.
func TestFrontendKeysExist(t *testing.T) {
	src, err := os.ReadFile("../../frontend/src/main.js")
	if err != nil {
		t.Skipf("frontend source not available: %v", err)
	}
	en := Dump("en")
	re := regexp.MustCompile(`\bT\('([A-Za-z0-9_.]+)'`)
	seen := 0
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		seen++
		if strings.HasSuffix(m[1], ".") {
			continue // dynamic key such as T('st.' + status); covered below
		}
		if _, ok := en[m[1]]; !ok {
			t.Errorf("main.js uses T('%s') but no such string exists", m[1])
		}
	}
	if seen == 0 {
		t.Error("main.js does not call T() at all; the UI is not translated")
	}

	// Keys the UI builds at run time.
	for _, k := range []string{
		"st.running", "st.paused", "st.queued", "st.downloading", "st.uploading", "st.error", "st.ready",
		"run.always", "run.auto", "run.never",
	} {
		if _, ok := en[k]; !ok {
			t.Errorf("dynamic key %q has no English string", k)
		}
	}
}
