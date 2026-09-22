// Package catalog is the list of BOINC projects a user can pick from when
// attaching, like the BOINC manager's "Add project" wizard.
//
// The list comes from BOINC's official project list (boinc.berkeley.edu). A
// snapshot is embedded so the picker works offline and on first start; it is
// refreshed from the network in the background and cached on disk.
package catalog

import (
	"context"
	_ "embed"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// OfficialListURL is where BOINC publishes its list of projects.
const OfficialListURL = "https://boinc.berkeley.edu/project_list.php"

//go:embed projects.json
var embedded []byte

// Project is one entry of the catalog.
type Project struct {
	Name        string   `json:"name"`
	URL         string   `json:"url"`
	WebURL      string   `json:"web,omitempty"`
	Area        string   `json:"area,omitempty"`
	SubArea     string   `json:"sub,omitempty"`
	Description string   `json:"description,omitempty"`
	Home        string   `json:"home,omitempty"`
	Image       string   `json:"image,omitempty"`
	Platforms   []string `json:"platforms,omitempty"` // BOINC platform names, plan classes stripped
	// Source is "official" for BOINC's list and "community" for projects added
	// by hand after checking that their server answers.
	Source string `json:"source"`
}

// Embedded returns the snapshot compiled into the program.
func Embedded() []Project {
	var ps []Project
	if err := json.Unmarshal(embedded, &ps); err != nil {
		return nil
	}
	return ps
}

// ---------------------------------------------------------------- parsing

type xmlList struct {
	Projects []xmlProject `xml:"project"`
}

type xmlProject struct {
	Name        string   `xml:"name"`
	URL         string   `xml:"url"`
	WebURL      string   `xml:"web_url"`
	Area        string   `xml:"general_area"`
	SubArea     string   `xml:"specific_area"`
	Description string   `xml:"description"`
	Home        string   `xml:"home"`
	Image       string   `xml:"image"`
	Platforms   []string `xml:"platforms>name"`
}

// latin1Reader converts the ISO-8859-1 the BOINC list is served in to UTF-8.
type latin1Reader struct{ r io.Reader }

func (l latin1Reader) Read(p []byte) (int, error) {
	// Each input byte can grow to two output bytes.
	buf := make([]byte, len(p)/2)
	n, err := l.r.Read(buf)
	out := 0
	for _, b := range buf[:n] {
		if b < 0x80 {
			p[out] = b
			out++
		} else {
			p[out] = 0xC0 | b>>6
			p[out+1] = 0x80 | b&0x3F
			out += 2
		}
	}
	return out, err
}

func charsetReader(label string, input io.Reader) (io.Reader, error) {
	switch strings.ToLower(label) {
	case "iso-8859-1", "latin1", "windows-1252", "us-ascii":
		return latin1Reader{input}, nil
	}
	return nil, fmt.Errorf("unsupported charset %q", label)
}

// ParseOfficial reads the XML of project_list.php.
func ParseOfficial(r io.Reader) ([]Project, error) {
	dec := xml.NewDecoder(r)
	dec.CharsetReader = charsetReader
	var l xmlList
	if err := dec.Decode(&l); err != nil {
		return nil, fmt.Errorf("project list: %w", err)
	}
	var out []Project
	for _, x := range l.Projects {
		p := Project{
			Name: clean(x.Name), URL: strings.TrimSpace(x.URL), WebURL: strings.TrimSpace(x.WebURL),
			Area: clean(x.Area), SubArea: clean(x.SubArea), Description: clean(x.Description),
			Home: clean(x.Home), Image: strings.TrimSpace(x.Image),
			Platforms: BasePlatforms(x.Platforms), Source: "official",
		}
		if p.Name == "" || !validURL(p.URL) {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

func clean(s string) string { return strings.Join(strings.Fields(s), " ") }

func validURL(u string) bool {
	return strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://")
}

// BasePlatforms strips plan classes ("windows_x86_64[cuda102]" -> "windows_x86_64")
// and returns the distinct platform names, sorted.
func BasePlatforms(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range in {
		p, _, _ = strings.Cut(strings.TrimSpace(p), "[")
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// Supports reports whether the project has applications for the platform.
func (p Project) Supports(platform string) bool {
	for _, x := range p.Platforms {
		if x == platform {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- loading

// Merge combines a fresh official list with the embedded community entries:
// official entries win, community ones are kept unless the official list has
// the same address. The result is sorted by name.
func Merge(official, fallback []Project) []Project {
	byURL := map[string]bool{}
	var out []Project
	for _, p := range official {
		byURL[normURL(p.URL)] = true
		out = append(out, p)
	}
	for _, p := range fallback {
		if p.Source == "community" && !byURL[normURL(p.URL)] {
			out = append(out, p)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func normURL(u string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(u), "/"))
}

// Fetch downloads the official list.
func Fetch(ctx context.Context, url string) ([]Project, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("project list: HTTP %d", resp.StatusCode)
	}
	ps, err := ParseOfficial(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if len(ps) < 5 { // a truncated or hijacked answer must not replace the list
		return nil, fmt.Errorf("project list: only %d projects", len(ps))
	}
	return ps, nil
}

const (
	userAgent = "Iris-catalog/1"
	cacheName = "project_catalog.json"
	cacheTTL  = 24 * time.Hour
)

// Store serves the catalog: the freshest of cache and snapshot right away,
// refreshed from the network in the background when the cache is stale.
type Store struct {
	dir string
	url string

	mu         sync.Mutex
	refreshing bool
}

func NewStore(cacheDir string) *Store { return &Store{dir: cacheDir, url: OfficialListURL} }

// Projects returns the catalog immediately and starts a refresh if the cached
// copy is older than a day (or missing).
func (s *Store) Projects() []Project {
	ps, age := s.readCache()
	if ps == nil {
		ps = Embedded()
		age = cacheTTL + time.Second
	}
	if age > cacheTTL {
		go s.Refresh(context.Background())
	}
	return ps
}

// Refresh fetches the official list and stores it; it reports whether the
// catalog changed.
func (s *Store) Refresh(ctx context.Context) error {
	s.mu.Lock()
	if s.refreshing {
		s.mu.Unlock()
		return nil
	}
	s.refreshing = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.refreshing = false; s.mu.Unlock() }()

	official, err := Fetch(ctx, s.url)
	if err != nil {
		return err
	}
	merged := Merge(official, Embedded())
	data, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(s.dir, cacheName+".tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.dir, cacheName))
}

func (s *Store) readCache() ([]Project, time.Duration) {
	path := filepath.Join(s.dir, cacheName)
	st, err := os.Stat(path)
	if err != nil {
		return nil, 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0
	}
	var ps []Project
	if json.Unmarshal(data, &ps) != nil || len(ps) == 0 {
		return nil, 0
	}
	return ps, time.Since(st.ModTime())
}
