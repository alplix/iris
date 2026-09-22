package assistant

import (
	"fmt"
	"strings"
	"testing"

	"github.com/alplix/iris/internal/app"
)

func TestBuildDigestWithNoHosts(t *testing.T) {
	dir := t.TempDir()
	for _, k := range []string{"APPDATA", "XDG_CONFIG_HOME", "HOME", "USERPROFILE"} {
		t.Setenv(k, dir)
	}
	mgr := app.NewManager() // IRIS_DEMO unset: starts empty
	if got := buildDigest(mgr); !strings.Contains(got, "no servers") {
		t.Errorf("digest = %q", got)
	}
}

func TestBuildDigestCapsHostsAndProjects(t *testing.T) {
	mgr := newTestManager(t)
	for i := 0; i < 10; i++ {
		h := mgr.Store.Upsert(app.HostCfg{Name: fmt.Sprintf("Extra %02d", i), Host: "localhost", Port: 31416, Demo: true})
		mgr.Refresh(h.ID)
	}

	digest := buildDigest(mgr)
	hostLines := strings.Count(digest, "Host \"")
	if hostLines > maxDigestHosts {
		t.Errorf("digest lists %d hosts, want at most %d", hostLines, maxDigestHosts)
	}
	if !strings.Contains(digest, "more servers not shown") {
		t.Error("an 11-host fleet must say some were left out, not silently drop them")
	}
}

func TestBuildDigestNeverIncludesAPassword(t *testing.T) {
	mgr := newTestManager(t)
	h := mgr.Store.List()[0]
	h.Password = "super-secret-rpc-password"
	mgr.Store.Upsert(h)

	digest := buildDigest(mgr)
	if strings.Contains(digest, "super-secret-rpc-password") {
		t.Fatal("a host's RPC password leaked into the digest sent to the assistant server")
	}
}

func TestBuildDigestMarksOfflineHosts(t *testing.T) {
	mgr := newTestManager(t)
	h := mgr.Store.Upsert(app.HostCfg{Name: "Unreachable", Host: "127.0.0.1", Port: 1})
	mgr.Refresh(h.ID)

	digest := buildDigest(mgr)
	if !strings.Contains(digest, `Host "Unreachable"`) || !strings.Contains(digest, "offline") {
		t.Errorf("digest should mark the unreachable host offline:\n%s", digest)
	}
}

// TestBuildDigestIncludesHardwareFacts guards the assistant's ability to
// answer "what CPU/GPU/RAM does this machine have" — previously the digest
// only covered fleet/task status, so the model had no way to know and would
// (correctly, but unhelpfully) say it couldn't check.
func TestBuildDigestIncludesHardwareFacts(t *testing.T) {
	mgr := newTestManager(t)
	digest := buildDigest(mgr)
	for _, want := range []string{"HW:", "Ryzen", "cores", "RAM", "GeForce RTX 4080 SUPER"} {
		if !strings.Contains(digest, want) {
			t.Errorf("digest should mention %q (real hardware facts):\n%s", want, digest)
		}
	}
}

// TestBuildDigestSurfacesTheMostRecentHighPriorityMessage guards the
// assistant's ability to help troubleshoot ("what's wrong with my client")
// without the person having to open the Messages page themselves.
func TestBuildDigestSurfacesTheMostRecentHighPriorityMessage(t *testing.T) {
	mgr := newTestManager(t)
	digest := buildDigest(mgr)
	if !strings.Contains(digest, "Recent issue: Computation for task brca_hydrogen_88 failed") {
		t.Errorf("digest should surface the demo host's error-priority message:\n%s", digest)
	}
}
