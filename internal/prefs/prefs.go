// Package prefs stores the client's global preference overrides
// (global_prefs_override.xml) and derives the limits the engines enforce.
package prefs

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const fileName = "global_prefs_override.xml"

var keyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

const maxValueLen = 256

type Store struct {
	mu   sync.RWMutex
	path string
	vals map[string]string
}

// Open loads the override file from dir; a missing or unreadable file yields
// an empty store.
func Open(dir string) *Store {
	s := &Store{path: filepath.Join(dir, fileName), vals: map[string]string{}}
	if data, err := os.ReadFile(s.path); err == nil {
		s.vals = parse(data)
	}
	return s
}

func parse(data []byte) map[string]string {
	out := map[string]string{}
	dec := xml.NewDecoder(bytes.NewReader(data))
	depth := 0
	var key string
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth == 2 {
				key = t.Name.Local
			}
		case xml.CharData:
			if depth == 2 && key != "" {
				if v := strings.TrimSpace(string(t)); v != "" {
					out[key] = v
				}
			}
		case xml.EndElement:
			if depth == 2 {
				key = ""
			}
			depth--
		}
	}
	return out
}

// Get returns a copy of the current overrides.
func (s *Store) Get() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.vals))
	for k, v := range s.vals {
		out[k] = v
	}
	return out
}

// Set replaces every override with pairs. Pairs with an empty value are
// dropped, so an empty list resets the client to its defaults.
func (s *Store) Set(pairs [][2]string) error {
	next := map[string]string{}
	for _, p := range pairs {
		k, v := strings.TrimSpace(p[0]), strings.TrimSpace(p[1])
		if !keyRe.MatchString(k) {
			return fmt.Errorf("invalid preference name %q", p[0])
		}
		if len(v) > maxValueLen {
			return fmt.Errorf("value of %s is too long", k)
		}
		if v == "" {
			continue
		}
		next[k] = v
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(s.path, marshal(next), 0o644); err != nil {
		return err
	}
	s.vals = next
	return nil
}

// XML renders the overrides in the layout the get_global_prefs_override RPC
// returns.
func (s *Store) XML() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return marshal(s.vals)
}

func marshal(vals map[string]string) []byte {
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b bytes.Buffer
	b.WriteString("<global_prefs_override>\n")
	for _, k := range keys {
		b.WriteString(" <" + k + ">")
		xml.EscapeText(&b, []byte(vals[k]))
		b.WriteString("</" + k + ">\n")
	}
	b.WriteString("</global_prefs_override>\n")
	return b.Bytes()
}

// Float returns the numeric value of key.
func (s *Store) Float(key string) (float64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.vals[key]
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// RealAppsKey is the override name the Settings page's experimental "run
// real applications" toggle sets, read back via Bool. It lives here next to
// the other overrides rather than in its own file so enabling/disabling it
// goes through the exact same GUI RPC (Get/SetPrefsOverride) path as every
// other preference already does.
const RealAppsKey = "real_apps_enabled"

// Bool returns whether key is set to a truthy value ("1", "true", "yes").
func (s *Store) Bool(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	switch strings.ToLower(strings.TrimSpace(s.vals[key])) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

// BoolDefault is Bool for a preference that is on unless explicitly turned
// off: an unset key returns def, "0"/"false"/"no" is off, anything truthy on.
func (s *Store) BoolDefault(key string, def bool) bool {
	s.mu.RLock()
	v := strings.ToLower(strings.TrimSpace(s.vals[key]))
	s.mu.RUnlock()
	switch v {
	case "":
		return def
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

// MaxCPUs is how many tasks may run at once on a host with ncpu cores, after
// applying max_ncpus and max_ncpus_pct. It is never below 1.
func (s *Store) MaxCPUs(ncpu int) int {
	n := ncpu
	if v, ok := s.Float("max_ncpus_pct"); ok && v > 0 && v < 100 {
		n = int(float64(ncpu) * v / 100)
	}
	if v, ok := s.Float("max_ncpus"); ok && v > 0 && int(v) < n {
		n = int(v)
	}
	if n < 1 {
		n = 1
	}
	return n
}
