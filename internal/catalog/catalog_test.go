package catalog

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// project_list.php is served as ISO-8859-1; 0xE9 is "é".
const officialXML = "<?xml version=\"1.0\" encoding=\"ISO-8859-1\" ?>\n<projects>\n" +
	"<project><name>Rosetta@home</name><id>1</id><url>https://boinc.bakerlab.org/rosetta/</url><web_url>https://boinc.bakerlab.org/rosetta/</web_url>" +
	"<general_area>Biology and Medicine</general_area><specific_area>Protein folding</specific_area>" +
	"<description><![CDATA[Predict protein structures  for  th\xe9rapies]]></description><home>Baker Lab</home>" +
	"<platforms><name>windows_x86_64</name><name>windows_x86_64[cuda102]</name><name>x86_64-pc-linux-gnu</name></platforms></project>\n" +
	"<project><name>Einstein@home</name><url>https://einsteinathome.org/</url><general_area>Physical Science</general_area>" +
	"<platforms><name>x86_64-pc-linux-gnu[cuda]</name></platforms></project>\n" +
	"<project><name>No URL</name></project>\n" +
	"<project><name>Bad URL</name><url>ftp://example.org/</url></project>\n" +
	"<account_manager><name>Science United</name><url>https://scienceunited.org/</url></account_manager>\n" +
	"</projects>"

func TestParseOfficial(t *testing.T) {
	ps, err := ParseOfficial(strings.NewReader(officialXML))
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 {
		t.Fatalf("got %d projects, want 2 (entries without a usable URL and account managers are skipped): %+v", len(ps), ps)
	}
	r := ps[0]
	if r.Name != "Rosetta@home" || r.Area != "Biology and Medicine" || r.Source != "official" {
		t.Errorf("project = %+v", r)
	}
	if r.Description != "Predict protein structures for thérapies" {
		t.Errorf("description = %q (ISO-8859-1 must become UTF-8, whitespace collapsed)", r.Description)
	}
	if got := strings.Join(r.Platforms, ","); got != "windows_x86_64,x86_64-pc-linux-gnu" {
		t.Errorf("platforms = %q, want plan classes stripped and de-duplicated", got)
	}
	if !r.Supports("x86_64-pc-linux-gnu") || r.Supports("arm64-apple-darwin") {
		t.Error("Supports gives the wrong answer")
	}
}

func TestMergeKeepsCommunityEntriesAndLetsOfficialWin(t *testing.T) {
	official := []Project{{Name: "Zeta", URL: "https://z.example/"}, {Name: "alpha", URL: "https://a.example/", Source: "official"}}
	fallback := []Project{
		{Name: "Old alpha", URL: "https://A.example", Source: "community"}, // same address as an official entry
		{Name: "Beta", URL: "https://b.example/", Source: "community"},
		{Name: "Stale official", URL: "https://gone.example/", Source: "official"}, // dropped: not in the fresh list
	}
	got := Merge(official, fallback)
	var names []string
	for _, p := range got {
		names = append(names, p.Name)
	}
	if strings.Join(names, ",") != "alpha,Beta,Zeta" {
		t.Fatalf("merged = %v, want alpha,Beta,Zeta", names)
	}
}

func serve(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func manyProjects(n int) string {
	var b strings.Builder
	b.WriteString("<projects>")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "<project><name>P%d</name><url>https://p%d.example/</url></project>", i, i)
	}
	b.WriteString("</projects>")
	return b.String()
}

func TestFetchRejectsBadAnswers(t *testing.T) {
	if _, err := Fetch(context.Background(), serve(t, "", 500).URL); err == nil {
		t.Error("HTTP 500 must be an error")
	}
	if _, err := Fetch(context.Background(), serve(t, "<html>hello</html>", 200).URL); err == nil {
		t.Error("a page that is not the list must be an error")
	}
	if _, err := Fetch(context.Background(), serve(t, manyProjects(2), 200).URL); err == nil {
		t.Error("a suspiciously short list must not replace the catalog")
	}
	ps, err := Fetch(context.Background(), serve(t, manyProjects(8), 200).URL)
	if err != nil || len(ps) != 8 {
		t.Fatalf("valid list: %d projects, %v", len(ps), err)
	}
}

func TestStoreServesSnapshotThenCachesTheLiveList(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	s.url = serve(t, manyProjects(9), 200).URL

	first := s.Projects() // nothing cached yet: the embedded snapshot, and a refresh starts
	if len(first) == 0 || first[0].Name == "P0" {
		t.Fatalf("first call should return the embedded snapshot, got %d entries", len(first))
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(dir, cacheName)); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	second := s.Projects()
	found := false
	for _, p := range second {
		found = found || p.Name == "P8"
	}
	if !found {
		t.Fatalf("after the refresh the live list should be served, got %d entries", len(second))
	}
}

func TestStoreKeepsWorkingWhenTheNetworkIsDown(t *testing.T) {
	s := NewStore(t.TempDir())
	s.url = "http://127.0.0.1:1/" // nothing listens here
	if err := s.Refresh(context.Background()); err == nil {
		t.Error("refresh against a dead server must report an error")
	}
	if len(s.Projects()) == 0 {
		t.Error("the embedded snapshot must still be served")
	}
}

func TestEmbeddedSnapshotIsUsable(t *testing.T) {
	ps := Embedded()
	if len(ps) < 20 {
		t.Fatalf("snapshot has only %d projects", len(ps))
	}
	seen := map[string]bool{}
	names := map[string]bool{}
	for _, p := range ps {
		if p.Name == "" || !validURL(p.URL) {
			t.Errorf("bad entry %+v", p)
		}
		if p.Source != "official" && p.Source != "community" {
			t.Errorf("%s has source %q", p.Name, p.Source)
		}
		if seen[normURL(p.URL)] {
			t.Errorf("duplicate address %s", p.URL)
		}
		seen[normURL(p.URL)] = true
		names[p.Name] = true
	}
	for _, must := range []string{"Einstein@home", "Rosetta@home", "World Community Grid", "PrimeGrid", "LHC@home", "Milkyway@home"} {
		if !names[must] {
			t.Errorf("snapshot lacks %s", must)
		}
	}
}
