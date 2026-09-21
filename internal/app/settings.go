package app

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Settings holds the manager's own preferences (as opposed to the per-host
// list kept in hosts.json).
type Settings struct {
	// Lang is the UI language code; empty until the user has chosen one.
	Lang string `json:"lang,omitempty"`
}

func settingsPath() string { return filepath.Join(ConfigDir(), "settings.json") }

func LoadSettings() Settings { return loadSettingsFrom(settingsPath()) }

func SaveSettings(s Settings) error { return saveSettingsTo(settingsPath(), s) }

func loadSettingsFrom(path string) Settings {
	var s Settings
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	return s
}

func saveSettingsTo(path string, s Settings) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
