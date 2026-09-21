package local

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alplix/iris/internal/config"
)

func TestWriteConfigCreatesReadableDefaultsOnce(t *testing.T) {
	dir := t.TempDir()
	d := NewDaemon(Info{DataDir: dir})

	if err := d.WriteConfig("1.0.0"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(d.Config)
	if err != nil {
		t.Fatal(err)
	}
	def := config.Default()
	if cfg.GPUCache != def.GPUCache || cfg.Options.MaxAppClients != def.Options.MaxAppClients {
		t.Fatalf("the client must read back the defaults the manager wrote, got %+v", cfg)
	}

	// The user's edits survive the next start.
	edited := def
	edited.Options.AllowRemoteGuiRPC = false
	if err := config.Save(edited, d.Config); err != nil {
		t.Fatal(err)
	}
	if err := d.WriteConfig("1.0.0"); err != nil {
		t.Fatal(err)
	}
	cfg, _ = config.Load(d.Config)
	if cfg.Options.AllowRemoteGuiRPC {
		t.Fatal("WriteConfig overwrote the user's cc_config.xml")
	}
}

func TestReadPassword(t *testing.T) {
	dir := t.TempDir()
	if got := ReadPassword(dir); got != "" {
		t.Fatalf("no file: got %q", got)
	}
	os.WriteFile(filepath.Join(dir, "gui_rpc_auth.cfg"), []byte("  abc123\r\n"), 0o600)
	if got := ReadPassword(dir); got != "abc123" {
		t.Fatalf("got %q, want abc123", got)
	}
}
