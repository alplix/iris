package main

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var cpidRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// loadOrCreateHostCPID returns this installation's cross-project host ID.
//
// It is random and kept in the client's own data directory, like BOINC does
// for its own ID: a stock BOINC client on the same machine therefore always
// appears to the project servers as a different host, and no hardware
// identifier ever leaves the machine.
func loadOrCreateHostCPID(dataDir string) string {
	fp := filepath.Join(dataDir, "host_cpid.txt")
	if data, err := os.ReadFile(fp); err == nil {
		if id := strings.ToLower(strings.TrimSpace(string(data))); cpidRe.MatchString(id) {
			return id
		}
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		fatal("cannot generate host id: %v", err)
	}
	sum := md5.Sum(b[:])
	id := hex.EncodeToString(sum[:])
	if err := os.WriteFile(fp, []byte(id+"\n"), 0o644); err != nil {
		fatal("cannot write %s: %v", fp, err)
	}
	return id
}
