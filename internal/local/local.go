package local

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/alplix/iris/internal/config"
)

type Info struct {
	Found   bool
	Bundled bool
	Exe     string
	DataDir string
	Hint    string
}

type DaemonStatus int

const (
	DaemonStopped DaemonStatus = iota
	DaemonRunning
	DaemonUnknown
)

func (s DaemonStatus) String() string {
	switch s {
	case DaemonRunning:
		return "running"
	case DaemonStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

type Daemon struct {
	Info    Info
	PIDFile string
	Config  string
}

func NewDaemon(info Info) *Daemon {
	d := &Daemon{Info: info}
	if info.DataDir != "" {
		d.PIDFile = filepath.Join(info.DataDir, "iris.pid")
		d.Config = filepath.Join(info.DataDir, "cc_config.xml")
	}
	return d
}

func (d *Daemon) Status() DaemonStatus {
	if d.PIDFile == "" {
		return DaemonUnknown
	}
	data, err := os.ReadFile(d.PIDFile)
	if err != nil {
		return DaemonStopped
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return DaemonStopped
	}
	return statusByPID(pid)
}

func (d *Daemon) PID() int {
	if d.PIDFile == "" {
		return 0
	}
	data, err := os.ReadFile(d.PIDFile)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return pid
}

// WriteConfig creates a default cc_config.xml, in the layout the client itself
// reads, when none exists yet. An existing file is the user's and is left alone.
func (d *Daemon) WriteConfig(version string) error {
	if d.Config == "" {
		return nil
	}
	if _, err := os.Stat(d.Config); err == nil {
		return nil
	}
	return config.Save(config.Default(), d.Config)
}

func Detect() Info {
	var candidates []string
	dataDir := config.DataDir()
	switch runtime.GOOS {
	case "windows":
		candidates = []string{
			filepath.Join(exeDir(), "irisd.exe"),
			`C:\Program Files\Iris\irisd.exe`,
			filepath.Join(config.DataDir(), "irisd.exe"),
		}
	case "darwin":
		candidates = []string{
			filepath.Join(exeDir(), "irisd"),
			"/Applications/Iris.app/Contents/Resources/irisd",
		}
	default:
		candidates = []string{
			filepath.Join(exeDir(), "irisd"),
			"/usr/bin/irisd",
			"/usr/local/bin/irisd",
		}
		if runtime.GOOS == "freebsd" {
			candidates = append(candidates, "/usr/local/bin/irisd")
		}
	}
	for i, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return Info{
				Found:   true,
				Bundled: i <= 1 && runtime.GOOS != "linux" || filepath.Dir(p) == exeDir(),
				Exe:     p,
				DataDir: dataDir,
			}
		}
	}
	return Info{
		Hint:    "Install the Iris client, or connect to remote hosts from Servers.",
		DataDir: dataDir,
	}
}

func ReadPassword(dataDir string) string {
	if dataDir == "" {
		return ""
	}
	p := filepath.Join(dataDir, "gui_rpc_auth.cfg")
	data, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	s := string(data)
	s = strings.TrimSpace(s)
	return s
}

func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(exe)
}
