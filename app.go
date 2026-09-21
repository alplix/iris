package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/alplix/iris/internal/app"
	"github.com/alplix/iris/internal/i18n"
	"github.com/alplix/iris/internal/local"
	"github.com/alplix/iris/internal/product"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	appAuthor = "Alperen Yavuz"
)

type App struct {
	ctx     context.Context
	mgr     *app.Manager
	daemon  *local.Daemon
	daemonI local.Info
	mu      sync.Mutex
}

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.mgr = app.NewManager()
	a.daemonI = local.Detect()
	if a.daemonI.Found {
		a.daemon = local.NewDaemon(a.daemonI)
	}
	a.autoConnectLocal()
	a.mgr.OnNotice(func(n app.Notice) {
		wailsruntime.EventsEmit(a.ctx, "notice", n)
	})
	a.mgr.Start()
}

const localHostID = "local-iris"

// autoConnectLocal makes the client that ships with Iris show up on its own,
// like the BOINC manager does for its local client: it adds the host, starts
// the client unless the user stopped it, and keeps the host's RPC password in
// step with the one the client generates.
func (a *App) autoConnectLocal() {
	if !a.daemonI.Found || a.daemonI.DataDir == "" {
		return
	}
	known := false
	for _, h := range a.mgr.Store.List() {
		if h.ID == localHostID || (isLoopback(h.Host) && h.Port == product.DefaultGUIRPCPort) {
			known = true
			break
		}
	}
	if !known {
		a.mgr.Store.Upsert(app.HostCfg{
			ID:       localHostID,
			Name:     "Local Iris",
			Host:     "localhost",
			Port:     product.DefaultGUIRPCPort,
			Password: local.ReadPassword(a.daemonI.DataDir),
		})
		_ = a.mgr.Store.Save()
	}
	if a.daemon != nil && a.daemon.Status() != local.DaemonRunning && !app.LoadSettings().ClientStopped {
		_ = local.StartDaemon(a.daemon, product.Version)
	}
	go a.syncLocalPassword()
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// syncLocalPassword copies the client's password into the local host. On a
// first run the client creates it a few seconds after it starts.
func (a *App) syncLocalPassword() {
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if pass := local.ReadPassword(a.daemonI.DataDir); pass != "" {
			h, ok := a.mgr.Store.Get(localHostID)
			if !ok {
				return // the user removed it
			}
			if h.Password != pass {
				h.Password = pass
				a.mgr.Store.Upsert(h)
				_ = a.mgr.Store.Save()
				a.mgr.Kick(localHostID)
			}
			return
		}
		time.Sleep(2 * time.Second)
	}
}

func (a *App) shutdown(ctx context.Context) {
	a.mgr.Stop()
}

type HostCfgJSON struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Password string `json:"password"`
	Demo     bool   `json:"demo"`
}

func (a *App) GetHosts() []HostCfgJSON {
	list := a.mgr.Store.List()
	out := make([]HostCfgJSON, len(list))
	for i, h := range list {
		out[i] = HostCfgJSON{ID: h.ID, Name: h.Name, Host: h.Host, Port: h.Port, Password: h.Password, Demo: h.Demo}
	}
	return out
}

func (a *App) AddHost(name, host string, port int, password string) HostCfgJSON {
	h := a.mgr.Store.Upsert(app.HostCfg{Name: name, Host: host, Port: port, Password: password})
	_ = a.mgr.Store.Save()
	a.mgr.Kick(h.ID)
	return HostCfgJSON{ID: h.ID, Name: h.Name, Host: h.Host, Port: h.Port, Password: h.Password, Demo: h.Demo}
}

func (a *App) UpdateHost(id, name, host string, port int, password string) HostCfgJSON {
	h := a.mgr.Store.Upsert(app.HostCfg{ID: id, Name: name, Host: host, Port: port, Password: password})
	_ = a.mgr.Store.Save()
	a.mgr.Kick(h.ID)
	return HostCfgJSON{ID: h.ID, Name: h.Name, Host: h.Host, Port: h.Port, Password: h.Password, Demo: h.Demo}
}

func (a *App) RemoveHost(id string) {
	a.mgr.Store.Remove(id)
	_ = a.mgr.Store.Save()
}

// GetVersion returns app identity information for the About section.
func (a *App) GetVersion() map[string]string {
	return map[string]string{
		"name":    product.Name,
		"version": product.Version,
		"author":  appAuthor,
		"repo":    product.RepoURL,
	}
}

// RefreshAll re-polls every configured host immediately.
func (a *App) RefreshAll() int {
	return a.mgr.RefreshAll()
}

// ClientOpAll applies a client operation (run mode, network mode, benchmarks)
// across the whole fleet and reports per-host failures.
func (a *App) ClientOpAll(op, mode string) map[string]string {
	return a.mgr.ClientOpAll(op, mode)
}

func (a *App) GetSnapshot(id string) *app.Snapshot {
	return a.mgr.Snap(id)
}

func (a *App) GetAllSnapshots() map[string]*app.Snapshot {
	return a.mgr.AllSnaps()
}

func (a *App) GetHistory(id string) []app.HistPoint {
	return a.mgr.History(id)
}

func (a *App) TaskOp(hostID, name, op string) error {
	return a.mgr.TaskOp(hostID, name, op)
}

func (a *App) ProjectOp(hostID, url, op string) error {
	return a.mgr.ProjectOp(hostID, url, op)
}

func (a *App) TransferOp(hostID, name, op string) error {
	return a.mgr.TransferOp(hostID, name, op)
}

func (a *App) ClientOp(hostID, op, mode string) error {
	return a.mgr.ClientOp(hostID, op, mode)
}

func (a *App) Attach(hostID, url, auth, name string) error {
	return a.mgr.Attach(hostID, url, auth, name)
}

func (a *App) GetPrefs(hostID string) (map[string]string, error) {
	return a.mgr.PrefsGet(hostID)
}

func (a *App) SetPrefs(hostID string, fields [][2]string) error {
	return a.mgr.PrefsSet(hostID, fields)
}

func (a *App) GetStats(hostID string) ([]app.StatSeries, error) {
	return a.mgr.Stats(hostID)
}

func (a *App) GetXferHistory(hostID string) ([]app.XferPoint, error) {
	return a.mgr.XferHistory(hostID)
}

func (a *App) GetDiskUsage(hostID string) (*app.DiskInfo, error) {
	return a.mgr.DiskUsage(hostID)
}

func (a *App) TestHost(host string, port int, password string) (string, error) {
	return a.mgr.TestHost(app.HostCfg{Host: host, Port: port, Password: password})
}

func (a *App) LookupAccount(baseURL, email, pass string) (string, error) {
	return a.mgr.LookupAccount(baseURL, email, pass)
}

func (a *App) DetectDaemon() map[string]interface{} {
	return map[string]interface{}{
		"found":   a.daemonI.Found,
		"exe":     a.daemonI.Exe,
		"dataDir": a.daemonI.DataDir,
		"hint":    a.daemonI.Hint,
	}
}

func (a *App) GetDaemonStatus() string {
	if a.daemon == nil {
		return "unknown"
	}
	return a.daemon.Status().String()
}

func (a *App) StartDaemon() error {
	if a.daemon == nil {
		return nil
	}
	st := app.LoadSettings()
	st.ClientStopped = false
	_ = app.SaveSettings(st)
	err := local.StartDaemon(a.daemon, product.Version)
	go a.syncLocalPassword()
	return err
}

func (a *App) StopDaemon() error {
	if a.daemon == nil {
		return nil
	}
	// Remember the choice: the manager must not restart what the user stopped.
	st := app.LoadSettings()
	st.ClientStopped = true
	_ = app.SaveSettings(st)
	return local.StopDaemon(a.daemon)
}

// GetLanguages lists the UI languages for the picker.
func (a *App) GetLanguages() []i18n.Language {
	return i18n.Languages()
}

// GetLanguage returns the language the user picked, or "" if they never did
// (the UI then follows the system language).
func (a *App) GetLanguage() string {
	if code := app.LoadSettings().Lang; i18n.Has(code) {
		return code
	}
	return ""
}

// SetLanguage remembers the user's language choice.
func (a *App) SetLanguage(code string) error {
	if !i18n.Has(code) {
		return fmt.Errorf("unsupported language %q", code)
	}
	s := app.LoadSettings()
	s.Lang = code
	if err := app.SaveSettings(s); err != nil {
		return err
	}
	applyTrayLanguage(code)
	return nil
}

// GetTranslations returns every UI string for code, English filling any gaps.
func (a *App) GetTranslations(code string) map[string]string {
	return i18n.Dump(code)
}

func (a *App) FmtCredit(v float64) string   { return app.FmtNum(v) }
func (a *App) FmtBytes(b int64) string      { return app.FmtBytes(b) }
func (a *App) FmtDuration(s float64) string { return app.FmtDuration(s) }
func (a *App) FmtTime(t time.Time) string   { return app.FmtTime(t) }
func (a *App) FmtAgo(t time.Time) string    { return app.FmtAgo(t) }
func (a *App) ProjColor(url string) string  { return app.ProjColor(url) }

func (a *App) showWindow() {
	wailsruntime.WindowShow(a.ctx)
	wailsruntime.WindowUnminimise(a.ctx)
}

func (a *App) hideWindow() {
	wailsruntime.WindowHide(a.ctx)
}

func (a *App) GetHostInfo() map[string]interface{} {
	return map[string]interface{}{
		"version": product.Version,
		"name":    product.Name,
		"author":  appAuthor,
	}
}
