package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileReplacesWithoutLeavingATempFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f.txt")
	for _, content := range []string{"one", "two"} {
		if err := WriteFile(p, []byte(content), 0o644, false); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(p); string(got) != content {
			t.Fatalf("got %q, want %q", got, content)
		}
	}
	if _, err := os.Stat(p + ".tmp"); err == nil {
		t.Fatal("temporary file left behind")
	}
	if _, err := os.Stat(p + ".bak"); err == nil {
		t.Fatal("no backup was asked for")
	}
}

func TestWriteFileKeepsThePreviousVersionWhenAsked(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f.txt")
	WriteFile(p, []byte("old"), 0o644, true)
	WriteFile(p, []byte("new"), 0o644, true)
	if got, _ := os.ReadFile(p + ".bak"); string(got) != "old" {
		t.Fatalf("backup = %q, want the previous content", got)
	}
}

func TestWriteFileFailsCleanlyWhenTheDirectoryIsMissing(t *testing.T) {
	if err := WriteFile(filepath.Join(t.TempDir(), "nope", "f.txt"), []byte("x"), 0o644, false); err == nil {
		t.Fatal("expected an error")
	}
}
