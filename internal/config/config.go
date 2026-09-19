package config

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/alplix/iris/internal/product"
)

type Config struct {
	XMLName  xml.Name `xml:"cc_config"`
	Options  Options  `xml:"options"`
	LogFlags LogFlags `xml:"log_flags"`
	GPUCache GPUCache `xml:"gpu_cache"`
}

type Options struct {
	UserAgent              string `xml:"user_agent"`
	AllowRemoteGuiRPC      bool   `xml:"allow_remote_gui_rpc"`
	MaxAppClients          int    `xml:"max_app_clients"`
	ReportResultsEarly     bool   `xml:"report_results_early"`
	HttpTransferTimeout    int    `xml:"http_transfer_timeout"`
	HttpServersBusyTimeout int    `xml:"http_servers_busy_timeout"`
	DontContactRefSite     bool   `xml:"dont_contact_ref_site"`
	UseAllGPUs             bool   `xml:"use_all_gpus"`
}

type LogFlags struct {
	XMLName xml.Name `xml:"log_flags"`
}

type GPUCache struct {
	Enabled          bool `xml:"enabled"`
	CacheSizeMB      int  `xml:"cache_size_mb"`
	CPUCacheSizeMB   int  `xml:"cpu_cache_size_mb"`
	SeparateSlots    bool `xml:"separate_slots"`
	SeparateProjects bool `xml:"separate_projects"`
}

func Default() *Config {
	return &Config{
		Options: Options{
			UserAgent:              product.UserAgent(),
			AllowRemoteGuiRPC:      true,
			MaxAppClients:          64,
			ReportResultsEarly:     true,
			HttpTransferTimeout:    30,
			HttpServersBusyTimeout: 30,
			DontContactRefSite:     true,
			UseAllGPUs:             true,
		},
		GPUCache: GPUCache{
			Enabled:          true,
			CacheSizeMB:      2048,
			CPUCacheSizeMB:   4096,
			SeparateSlots:    true,
			SeparateProjects: true,
		},
	}
}

func DataDir() string {
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(os.Getenv("ProgramData"), "Iris")
	case "darwin":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Application Support", "Iris")
	default:
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".local", "share", "iris")
	}
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Default(), nil
	}
	var cfg Config
	if err := xml.Unmarshal(data, &cfg); err != nil {
		return Default(), nil
	}
	return &cfg, nil
}

func Save(cfg *Config, path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := xml.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	var buf strings.Builder
	buf.WriteString(xml.Header)
	buf.Write(data)
	buf.WriteString("\n")
	return os.WriteFile(path, []byte(buf.String()), 0o644)
}
