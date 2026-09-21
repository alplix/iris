package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alplix/iris/internal/config"
	"github.com/alplix/iris/internal/product"
)

// Iris must be able to run next to a stock BOINC client: it may not share its
// port, its data directory or its host identity.

func TestDefaultsDoNotOverlapWithBOINC(t *testing.T) {
	t.Setenv("IRIS_GUI_RPC_PORT", "")
	if got := guiRPCPort(); got != product.DefaultGUIRPCPort || got == product.BOINCGUIRPCPort {
		t.Fatalf("default GUI RPC port = %d, must differ from BOINC's %d", got, product.BOINCGUIRPCPort)
	}
	dir := strings.ToLower(config.DataDir())
	if strings.Contains(dir, "boinc") {
		t.Fatalf("data directory %q must not be BOINC's", dir)
	}
	if filepath.Base(dir) != "iris" {
		t.Fatalf("data directory %q should be Iris' own", dir)
	}
}

func TestBOINCPortIsNeverBound(t *testing.T) {
	t.Setenv("IRIS_GUI_RPC_PORT", "31416")
	if got := guiRPCPort(); got != product.DefaultGUIRPCPort {
		t.Fatalf("IRIS_GUI_RPC_PORT=31416 gave %d; BOINC's port must be refused", got)
	}
	t.Setenv("IRIS_GUI_RPC_PORT", "40000")
	if got := guiRPCPort(); got != 40000 {
		t.Fatalf("a custom port should be honoured, got %d", got)
	}
}

func TestHostCPIDIsPrivateStableAndPerInstall(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	id := loadOrCreateHostCPID(a)
	if !cpidRe.MatchString(id) {
		t.Fatalf("host id %q is not 32 lowercase hex characters", id)
	}
	if again := loadOrCreateHostCPID(a); again != id {
		t.Fatalf("host id changed between runs: %s -> %s", id, again)
	}
	if other := loadOrCreateHostCPID(b); other == id {
		t.Fatal("two installations must not share a host id")
	}

	// A damaged file is replaced rather than sent to project servers.
	if err := os.WriteFile(filepath.Join(a, "host_cpid.txt"), []byte("not-an-id"), 0o644); err != nil {
		t.Fatal(err)
	}
	if fixed := loadOrCreateHostCPID(a); !cpidRe.MatchString(fixed) {
		t.Fatalf("damaged host id was not replaced: %q", fixed)
	}
}
