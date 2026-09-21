package worker

import (
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeDownloader struct{ content string }

func (f fakeDownloader) DownloadFile(projectURL, filename, dest string) error {
	return os.WriteFile(dest, []byte(f.content), 0o644)
}

func (f fakeDownloader) DownloadFileByURL(url, dest string) error {
	return os.WriteFile(dest, []byte(f.content), 0o644)
}

func sum(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

func TestDownloadFilesVerifiesMD5(t *testing.T) {
	slot := t.TempDir()
	e := &Engine{dl: fakeDownloader{content: "payload"}}

	good := ResultSnapshot{Slot: slot, Files: []FileRef{{Name: "app", URL: "http://x/app", MD5: strings.ToUpper(sum("payload"))}}}
	if err := e.downloadFiles(good); err != nil {
		t.Fatalf("matching MD5 must pass: %v", err)
	}

	bad := ResultSnapshot{Slot: slot, Files: []FileRef{{Name: "evil", URL: "http://x/evil", MD5: sum("something else")}}}
	if err := e.downloadFiles(bad); err == nil {
		t.Fatal("a file with the wrong MD5 must be rejected")
	}
	if _, err := os.Stat(filepath.Join(slot, "evil")); !os.IsNotExist(err) {
		t.Error("a rejected file must be deleted so it can never be executed")
	}

	none := ResultSnapshot{Slot: slot, Files: []FileRef{{Name: "nohash", URL: "http://x/n"}}}
	if err := e.downloadFiles(none); err != nil {
		t.Fatalf("files without a hash are accepted: %v", err)
	}
}

func TestDownloadFilesSkipsVerifiedExistingFile(t *testing.T) {
	slot := t.TempDir()
	if err := os.WriteFile(filepath.Join(slot, "app"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The downloader would write different bytes; a verified file must not be
	// fetched again.
	e := &Engine{dl: fakeDownloader{content: "changed"}}
	r := ResultSnapshot{Slot: slot, Files: []FileRef{{Name: "app", URL: "http://x/app", MD5: sum("payload")}}}
	if err := e.downloadFiles(r); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(slot, "app")); string(b) != "payload" {
		t.Errorf("verified file was overwritten: %q", b)
	}
}
