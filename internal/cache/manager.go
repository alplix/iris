package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Manager struct {
	dataDir string
	gpuDir  string
	cpuDir  string
	cfg     Config
	mu      sync.RWMutex
}

type Config struct {
	Enabled          bool
	CacheSizeMB      int
	CPUCacheSizeMB   int
	SeparateSlots    bool
	SeparateProjects bool
}

type DiskProject struct {
	URL       string
	DiskUsage int64
	GPUDisk   int64
	CPUDisk   int64
}

type DiskInfo struct {
	Total    int64
	Free     int64
	Projects []DiskProject
}

func New(dataDir string, cfg Config) *Manager {
	m := &Manager{dataDir: dataDir, cfg: cfg}
	if cfg.SeparateSlots {
		m.gpuDir = filepath.Join(dataDir, "slots_gpu")
		m.cpuDir = filepath.Join(dataDir, "slots")
	} else {
		m.gpuDir = filepath.Join(dataDir, "slots")
		m.cpuDir = filepath.Join(dataDir, "slots")
	}
	return m
}

func (m *Manager) Init() error {
	dirs := []string{
		m.cpuDir, m.gpuDir,
		filepath.Join(m.dataDir, "projects"),
		filepath.Join(m.dataDir, "projects_gpu"),
		filepath.Join(m.dataDir, "cache"),
		filepath.Join(m.dataDir, "cache_gpu"),
		filepath.Join(m.dataDir, "templates"),
		filepath.Join(m.dataDir, "notices"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}
	return nil
}

func (m *Manager) SlotDir(gpu bool) string {
	if gpu && m.cfg.SeparateSlots {
		return m.gpuDir
	}
	return m.cpuDir
}

func (m *Manager) ProjectDir(gpu bool) string {
	if gpu && m.cfg.SeparateProjects {
		return filepath.Join(m.dataDir, "projects_gpu")
	}
	return filepath.Join(m.dataDir, "projects")
}

func (m *Manager) AllocSlot(gpu bool) (string, error) {
	base := m.SlotDir(gpu)
	for i := 0; i < 10000; i++ {
		slot := filepath.Join(base, fmt.Sprintf("%d", i))
		if _, err := os.Stat(slot); os.IsNotExist(err) {
			if err := os.MkdirAll(slot, 0o755); err != nil {
				return "", err
			}
			return slot, nil
		}
	}
	return "", fmt.Errorf("no free slots in %s", base)
}

func (m *Manager) FreeSlot(slot string) error {
	return os.RemoveAll(slot)
}

func (m *Manager) GetDiskUsage() *DiskInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var projects []DiskProject
	for _, projDir := range []string{
		filepath.Join(m.dataDir, "projects"),
		filepath.Join(m.dataDir, "projects_gpu"),
	} {
		entries, err := os.ReadDir(projDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			url := strings.ReplaceAll(name, "_", "/")
			if !strings.Contains(url, ".") {
				continue
			}
			size := dirSize(filepath.Join(projDir, name))
			dp := DiskProject{URL: url, DiskUsage: size}
			if strings.Contains(projDir, "gpu") {
				dp.GPUDisk = size
			} else {
				dp.CPUDisk = size
			}
			projects = append(projects, dp)
		}
	}

	return &DiskInfo{Projects: projects}
}

func dirSize(path string) int64 {
	var size int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		size += info.Size()
		return nil
	})
	return size
}
