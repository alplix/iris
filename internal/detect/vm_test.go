package detect

import "testing"

func TestParseVBoxAndDockerVersions(t *testing.T) {
	if got := parseVBoxVersion("7.0.14r161095\r\n"); got != "7.0.14" {
		t.Errorf("got %q", got)
	}
	if parseVBoxVersion("error: not found") != "" || parseVBoxVersion("") != "" {
		t.Error("garbage is no version")
	}
	if got := parseDockerVersion("Docker version 24.0.7, build afdd53b"); got != "24.0.7" {
		t.Errorf("got %q", got)
	}
	if parseDockerVersion("command not found") != "" {
		t.Error("garbage is no version")
	}
}
