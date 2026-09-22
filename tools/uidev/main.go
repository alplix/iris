// uidev serves the built frontend (frontend/dist) together with the manager's
// API over HTTP, so the real UI can be developed and screenshotted in a normal
// browser without the Wails shell. It uses simulated demo servers and an
// isolated settings directory: nothing of the user's real configuration is
// read or written.
//
//	go run ./tools/uidev [-addr 127.0.0.1:5188] [-dist frontend/dist]
//
// The page reads a few query parameters so screenshots can be scripted:
// page=tasks|projects|..., theme=light|dark, lang=de, modal=attach,
// q=<catalog search>, pick=1 (select the first catalog match).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/alplix/iris/internal/app"
	"github.com/alplix/iris/internal/assistant"
	"github.com/alplix/iris/internal/catalog"
	"github.com/alplix/iris/internal/i18n"
	"github.com/alplix/iris/internal/product"
	"github.com/alplix/iris/internal/tilvar"
)

type API struct {
	mgr *app.Manager
	cat *catalog.Store
	ast *assistant.Session
}

type hostJSON struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Password string `json:"password"`
	Demo     bool   `json:"demo"`
}

func toJSON(h app.HostCfg) hostJSON {
	return hostJSON{h.ID, h.Name, h.Host, h.Port, h.Password, h.Demo}
}

func (a *API) GetHosts() []hostJSON {
	var out []hostJSON
	for _, h := range a.mgr.Store.List() {
		out = append(out, toJSON(h))
	}
	return out
}
func (a *API) AddHost(name, host string, port int, pw string) hostJSON {
	h := a.mgr.Store.Upsert(app.HostCfg{Name: name, Host: host, Port: port, Password: pw})
	a.mgr.Kick(h.ID)
	return toJSON(h)
}
func (a *API) UpdateHost(id, name, host string, port int, pw string) hostJSON {
	h := a.mgr.Store.Upsert(app.HostCfg{ID: id, Name: name, Host: host, Port: port, Password: pw})
	a.mgr.Kick(h.ID)
	return toJSON(h)
}
func (a *API) RemoveHost(id string) { a.mgr.Store.Remove(id) }
func (a *API) GetVersion() map[string]string {
	return map[string]string{"name": product.Name, "version": product.Version, "author": "Alperen Yavuz", "repo": product.RepoURL}
}
func (a *API) RefreshAll() int                               { return a.mgr.RefreshAll() }
func (a *API) ClientOpAll(op, mode string) map[string]string { return a.mgr.ClientOpAll(op, mode) }
func (a *API) GetSnapshot(id string) *app.Snapshot           { return a.mgr.Snap(id) }
func (a *API) GetHistory(id string) []app.HistPoint          { return a.mgr.History(id) }
func (a *API) TaskOp(h, n, op string) error                  { return a.mgr.TaskOp(h, n, op) }
func (a *API) ProjectOp(h, u, op string) error               { return a.mgr.ProjectOp(h, u, op) }
func (a *API) TransferOp(h, n, op string) error              { return a.mgr.TransferOp(h, n, op) }
func (a *API) ClientOp(h, op, mode string) error             { return a.mgr.ClientOp(h, op, mode) }
func (a *API) Attach(h, u, au, n string) error               { return a.mgr.Attach(h, u, au, n) }
func (a *API) GetPrefs(h string) (map[string]string, error)  { return a.mgr.PrefsGet(h) }
func (a *API) SetPrefs(h string, f [][2]string) error        { return a.mgr.PrefsSet(h, f) }
func (a *API) GetStats(h string) ([]app.StatSeries, error)   { return a.mgr.Stats(h) }
func (a *API) GetXferHistory(h string) ([]app.XferPoint, error) {
	return a.mgr.XferHistory(h)
}
func (a *API) GetDiskUsage(h string) (*app.DiskInfo, error) { return a.mgr.DiskUsage(h) }
func (a *API) TestHost(host string, port int, pw string) (string, error) {
	return a.mgr.TestHost(app.HostCfg{Host: host, Port: port, Password: pw})
}
func (a *API) LookupAccount(u, e, p string) (string, error) { return a.mgr.LookupAccount(u, e, p) }
func (a *API) DetectDaemon() map[string]any {
	return map[string]any{"found": true, "exe": `C:\Program Files\alplix\Iris\irisd.exe`, "dataDir": `C:\ProgramData\Iris`, "hint": ""}
}
func (a *API) GetDaemonStatus() string                       { return "running" }
func (a *API) StartDaemon() error                            { return nil }
func (a *API) StopDaemon() error                             { return nil }
func (a *API) GetLanguages() []i18n.Language                 { return i18n.Languages() }
func (a *API) GetLanguage() string                           { return app.LoadSettings().Lang }
func (a *API) GetTranslations(code string) map[string]string { return i18n.Dump(code) }
func (a *API) SetLanguage(code string) error {
	if !i18n.Has(code) {
		return fmt.Errorf("unsupported language %q", code)
	}
	s := app.LoadSettings()
	s.Lang = code
	return app.SaveSettings(s)
}
func (a *API) GetProjectCatalog() []catalog.Project { return a.cat.Projects() }
func (a *API) GetProjectConfig(u string) (*catalog.Config, error) {
	return catalog.FetchConfig(context.Background(), u)
}
func (a *API) OpenURL(u string) error { fmt.Println("open:", u); return nil }

// uidevAssistantOn mirrors app.Settings.AssistantEnabled, kept in memory
// only (uidev never touches the real settings file).
var uidevAssistantOn bool

func (a *API) AssistantAvailable() bool { return tilvar.Available() }
func (a *API) AssistantEnabled() bool   { return tilvar.Available() && uidevAssistantOn }
func (a *API) SetAssistantEnabled(on bool) error {
	uidevAssistantOn = on
	a.ast.Reset()
	return nil
}
func (a *API) AssistantHistory() []tilvar.Message { return a.ast.History() }
func (a *API) AssistantAsk(text string) (assistant.Reply, error) {
	if !a.AssistantEnabled() {
		return assistant.Reply{}, fmt.Errorf("the assistant is turned off")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return a.ast.Ask(ctx, text)
}
func (a *API) AssistantConfirm(id string) error { return a.ast.Confirm(id) }
func (a *API) AssistantCancel(id string)        { a.ast.Cancel(id) }
func (a *API) AssistantReset()                  { a.ast.Reset() }

// stub makes window.go.main.App call the API above and applies the query
// parameters used for scripted screenshots.
const stub = `<script>
(() => {
  const q = new URLSearchParams(location.search)
  try { if (q.get('theme')) localStorage.setItem('iris-theme', q.get('theme')) } catch (e) {}
  const call = async (method, args) => {
    const r = await fetch('/rpc', { method: 'POST', body: JSON.stringify({ method, args }) })
    const j = await r.json()
    if (j.error) throw new Error(j.error)
    return j.result
  }
  window.go = { main: { App: new Proxy({}, { get: (_, method) => async (...args) => {
    if (method === 'GetLanguage' && q.get('lang')) return q.get('lang')
    return call(method, args)
  } }) } }
  window.addEventListener('load', () => setTimeout(async () => {
    const wait = ms => new Promise(r => setTimeout(r, ms))
    if (q.get('page')) { window._setPage(q.get('page')); await wait(1200) }
    if (q.get('modal') === 'attach') {
      await window._showAttachProject(); await wait(300)
      if (q.get('q')) { const i = document.getElementById('cat-q'); i.value = q.get('q'); i.dispatchEvent(new Event('input')) }
      if (q.get('pick')) { document.querySelectorAll('.cat-item')[parseInt(q.get('pick')) - 1]?.click(); await wait(2500) }
    }
    document.title = 'ready'
  }, 1800))
})()
</script>`

func main() {
	addr := flag.String("addr", "127.0.0.1:5188", "listen address")
	dist := flag.String("dist", "frontend/dist", "built frontend")
	flag.Parse()

	// Isolate every settings location the manager might touch.
	tmp, err := os.MkdirTemp("", "iris-uidev-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)
	for _, k := range []string{"APPDATA", "XDG_CONFIG_HOME", "HOME", "USERPROFILE"} {
		os.Setenv(k, tmp)
	}
	os.Setenv("IRIS_DEMO", "1")

	mgr := app.NewManager()
	api := &API{mgr: mgr, cat: catalog.NewStore(filepath.Join(tmp, "cat")), ast: assistant.NewSession(mgr)}
	if v := os.Getenv("IRIS_TILVAR_API_KEY"); v != "" {
		uidevAssistantOn = true // let a developer flip it on with a real key without clicking through Settings
	}
	api.mgr.Start()
	rv := reflect.ValueOf(api)

	mux := http.NewServeMux()
	mux.HandleFunc("/rpc", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string            `json:"method"`
			Args   []json.RawMessage `json:"args"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		reply := map[string]any{}
		m := rv.MethodByName(req.Method)
		if !m.IsValid() || len(req.Args) != m.Type().NumIn() {
			reply["error"] = "no such method or wrong arity: " + req.Method
		} else {
			in := make([]reflect.Value, len(req.Args))
			for i, raw := range req.Args {
				p := reflect.New(m.Type().In(i))
				if err := json.Unmarshal(raw, p.Interface()); err != nil {
					reply["error"] = err.Error()
				}
				in[i] = p.Elem()
			}
			if reply["error"] == nil {
				out := m.Call(in)
				for _, o := range out {
					if e, ok := o.Interface().(error); ok && e != nil {
						reply["error"] = e.Error()
					}
				}
				if len(out) > 0 && reply["error"] == nil {
					if _, isErr := out[0].Interface().(error); !isErr || out[0].IsNil() {
						reply["result"] = out[0].Interface()
					}
				}
			}
		}
		json.NewEncoder(w).Encode(reply)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			b, err := os.ReadFile(filepath.Join(*dist, "index.html"))
			if err != nil {
				http.Error(w, "build the frontend first: cd frontend && npm run build", 500)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(strings.Replace(string(b), "<head>", "<head>"+stub, 1)))
			return
		}
		http.FileServer(http.Dir(*dist)).ServeHTTP(w, r)
	})
	srv := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	fmt.Println("uidev on http://" + *addr)
	if err := srv.ListenAndServe(); err != nil {
		fmt.Println(err)
	}
}
