package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type HostCfg struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Password string `json:"password,omitempty"`
	Demo     bool   `json:"demo,omitempty"`
}

type Store struct {
	mu    sync.RWMutex
	path  string
	hosts []HostCfg
}

func ConfigDir() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "iris")
}

func LoadStore() *Store {
	p := filepath.Join(ConfigDir(), "hosts.json")
	s := &Store{path: p}
	data, err := os.ReadFile(p)
	if err == nil {
		_ = json.Unmarshal(data, &s.hosts)
	}
	return s
}

func (s *Store) Save() error {
	s.mu.RLock()
	data, err := json.MarshalIndent(s.hosts, "", "  ")
	s.mu.RUnlock()
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) List() []HostCfg {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]HostCfg, len(s.hosts))
	copy(out, s.hosts)
	return out
}

func (s *Store) Get(id string) (HostCfg, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, h := range s.hosts {
		if h.ID == id {
			return h, true
		}
	}
	return HostCfg{}, false
}

func (s *Store) Upsert(h HostCfg) HostCfg {
	s.mu.Lock()
	if h.ID != "" {
		for i := range s.hosts {
			if s.hosts[i].ID == h.ID {
				s.hosts[i] = h
				s.mu.Unlock()
				return h
			}
		}
	}
	h.ID = newID()
	s.hosts = append(s.hosts, h)
	s.mu.Unlock()
	return h
}

func (s *Store) Remove(id string) {
	s.mu.Lock()
	out := s.hosts[:0]
	for _, h := range s.hosts {
		if h.ID != id {
			out = append(out, h)
		}
	}
	s.hosts = out
	s.mu.Unlock()
}

var idCounter uint64

func newID() string {
	idCounter++
	return nowStamp() + "-" + itoa(int(idCounter))
}
