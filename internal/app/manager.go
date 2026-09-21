package app

import (
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/alplix/iris/internal/boinc"
	"github.com/alplix/iris/internal/product"
)

type HistPoint struct {
	T       time.Time `json:"t"`
	Running int       `json:"running"`
}

type Notice struct {
	Kind     string `json:"kind"`
	HostID   string `json:"hostId"`
	HostName string `json:"hostName"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	// Arg and Count let the UI phrase the notice in its own language; Title
	// and Body are the English fallback.
	Arg   string `json:"arg,omitempty"`
	Count int    `json:"count,omitempty"`
}

type Manager struct {
	Store *Store

	mu     sync.RWMutex
	mocks  map[string]*Mock
	snaps  map[string]*Snapshot
	hist   map[string][]HistPoint
	watch  map[string]*hostWatch
	subs   []func()
	notice func(Notice)
	stop   chan struct{}
	once   sync.Once
}

type hostWatch struct {
	seen    bool
	errs    int
	offline bool
	warned  map[string]bool
}

var Mgr *Manager

func NewManager() *Manager { return newManagerWith(LoadStore()) }

// newManagerWith builds a manager around store. The manager starts empty, like
// the BOINC manager: hosts come from the local client that is detected at
// startup and from the ones the user adds. Demo data is opt-in (IRIS_DEMO=1);
// demo hosts that earlier versions created on their own are dropped.
func newManagerWith(store *Store) *Manager {
	m := &Manager{
		Store: store,
		mocks: map[string]*Mock{},
		snaps: map[string]*Snapshot{},
		hist:  map[string][]HistPoint{},
		watch: map[string]*hostWatch{},
		stop:  make(chan struct{}),
	}
	wantDemo := os.Getenv("IRIS_DEMO") == "1"
	changed := false
	hasDemo := false
	for _, h := range m.Store.List() {
		if !h.Demo {
			continue
		}
		if wantDemo {
			hasDemo = true
		} else {
			m.Store.Remove(h.ID)
			changed = true
		}
	}
	if wantDemo && !hasDemo {
		m.Store.Upsert(HostCfg{Name: "Demo Server", Host: "localhost", Port: product.DefaultGUIRPCPort, Demo: true})
		changed = true
	}
	if changed {
		_ = m.Store.Save()
	}
	Mgr = m
	return m
}

func (m *Manager) OnNotice(fn func(Notice)) {
	m.mu.Lock()
	m.notice = fn
	m.mu.Unlock()
}

func (m *Manager) OnUpdate(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subs = append(m.subs, fn)
}

func (m *Manager) notifySubs() {
	m.mu.RLock()
	subs := append([]func(){}, m.subs...)
	m.mu.RUnlock()
	for _, fn := range subs {
		fn()
	}
}

func (m *Manager) Start() {
	go m.loop()
}

func (m *Manager) Stop() {
	m.once.Do(func() { close(m.stop) })
}

func (m *Manager) loop() {
	for _, h := range m.Store.List() {
		go m.Refresh(h.ID)
	}
	t := time.NewTicker(4 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-t.C:
			for _, h := range m.Store.List() {
				m.Refresh(h.ID)
			}
		}
	}
}

func (m *Manager) mockFor(cfg HostCfg) *Mock {
	m.mu.Lock()
	defer m.mu.Unlock()
	mk, ok := m.mocks[cfg.ID]
	if !ok {
		mk = NewMock(cfg)
		m.mocks[cfg.ID] = mk
	}
	return mk
}

func (m *Manager) clientFor(cfg HostCfg) (*boinc.Client, error) {
	if cfg.Demo {
		return nil, fmt.Errorf("demo host has no real RPC")
	}
	c := boinc.NewClient(cfg.Host, cfg.Port, cfg.Password)
	if err := c.Connect(); err != nil {
		return nil, err
	}
	return c, nil
}

func (m *Manager) Refresh(id string) {
	cfg, ok := m.Store.Get(id)
	if !ok {
		return
	}
	var snap *Snapshot
	if cfg.Demo {
		snap = m.mockFor(cfg).Snapshot()
	} else {
		snap = m.refreshReal(cfg)
	}
	m.mu.Lock()
	m.snaps[id] = snap
	if snap.Online {
		hp := append(m.hist[id], HistPoint{T: time.Now(), Running: snap.Totals.Running})
		if len(hp) > 900 {
			hp = hp[len(hp)-900:]
		}
		m.hist[id] = hp
	}
	hostName := cfg.Name
	note := m.notice
	m.mu.Unlock()

	if note != nil {
		m.checkNotices(snap, hostName, note)
	}
	m.notifySubs()
}

func (m *Manager) checkNotices(snap *Snapshot, hostName string, note func(Notice)) {
	type pend struct{ n Notice }
	var pending []pend

	m.mu.Lock()
	w := m.watch[snap.HostID]
	if w == nil {
		w = &hostWatch{warned: map[string]bool{}}
		m.watch[snap.HostID] = w
	}
	first := !w.seen
	perrs, poff := w.errs, w.offline
	w.seen = true
	w.errs = snap.Totals.Errors
	w.offline = !snap.Online
	if snap.Online {
		nowS := time.Now().Unix()
		for i := range snap.Tasks {
			tk := &snap.Tasks[i]
			if tk.Deadline == 0 || tk.Status == StatusReady || tk.Status == StatusError || tk.Status == StatusPaused {
				continue
			}
			left := tk.Deadline - nowS
			if left > 0 && left < 1800 && !w.warned[tk.Name] {
				w.warned[tk.Name] = true
				name := tk.Name
				if len(name) > 44 {
					name = name[:44]
				}
				pending = append(pending, pend{Notice{
					Kind: "deadline", HostID: snap.HostID, HostName: hostName,
					Title: "Deadline warning", Body: name + " is due soon", Arg: name,
				}})
			}
		}
	} else {
		for k := range w.warned {
			delete(w.warned, k)
		}
	}
	m.mu.Unlock()

	if first {
		return
	}
	if snap.Online && w.errs > perrs {
		pending = append(pending, pend{Notice{
			Kind: "error", HostID: snap.HostID, HostName: hostName,
			Title: "Task error", Body: hostName + ": " + itoa(w.errs-perrs) + " task(s) errored",
			Count: w.errs - perrs,
		}})
	}
	if !snap.Online && !poff {
		pending = append(pending, pend{Notice{
			Kind: "offline", HostID: snap.HostID, HostName: hostName,
			Title: "Host offline", Body: hostName + " is not reachable",
		}})
	}
	for _, p := range pending {
		note(p.n)
	}
}

func (m *Manager) refreshReal(cfg HostCfg) *Snapshot {
	c, err := m.clientFor(cfg)
	if err != nil {
		return offlineSnap(cfg, err.Error())
	}
	defer c.Close()
	st, err := c.GetState()
	if err != nil {
		return offlineSnap(cfg, err.Error())
	}
	fts, err := c.GetTransfers()
	if err != nil {
		fts = nil
	}
	cc, _ := c.GetCcStatus()
	msgs, err := c.GetMessages(-1)
	if err != nil || msgs == nil {
		msgs = nil
	}
	ver := c.Version
	if ver == "" {
		ver = st.HostInfo.BoincVer
	}
	return Normalize(cfg.ID, false, st, fts, cc, msgs, ver)
}

func offlineSnap(cfg HostCfg, errMsg string) *Snapshot {
	return &Snapshot{
		HostID: cfg.ID, Demo: cfg.Demo, Online: false,
		Error: errMsg, TS: time.Now(), Version: "",
	}
}

func (m *Manager) Snap(id string) *Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snaps[id]
}

func (m *Manager) History(id string) []HistPoint {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]HistPoint, len(m.hist[id]))
	copy(out, m.hist[id])
	return out
}

func (m *Manager) AllSnaps() map[string]*Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]*Snapshot, len(m.snaps))
	for k, v := range m.snaps {
		out[k] = v
	}
	return out
}

func (m *Manager) Kick(id string) {
	go m.Refresh(id)
}

// ---- Operations ----

func (m *Manager) TaskOp(hostID, name, op string) error {
	cfg, ok := m.Store.Get(hostID)
	if !ok {
		return fmt.Errorf("host not found")
	}
	var err error
	if cfg.Demo {
		err = m.mockFor(cfg).ResultOp(name, op)
	} else if c, cerr := m.clientFor(cfg); cerr != nil {
		err = cerr
	} else {
		err = c.ResultOp(name, op)
		c.Close()
	}
	m.Kick(hostID)
	return err
}

func (m *Manager) ProjectOp(hostID, url, op string) error {
	cfg, ok := m.Store.Get(hostID)
	if !ok {
		return fmt.Errorf("host not found")
	}
	var err error
	if cfg.Demo {
		err = m.mockFor(cfg).ProjectOp(url, op)
	} else if c, cerr := m.clientFor(cfg); cerr != nil {
		err = cerr
	} else {
		err = c.ProjectOp(url, op)
		c.Close()
	}
	m.Kick(hostID)
	return err
}

func (m *Manager) TransferOp(hostID, name, op string) error {
	cfg, ok := m.Store.Get(hostID)
	if !ok {
		return fmt.Errorf("host not found")
	}
	var err error
	if cfg.Demo {
		err = m.mockFor(cfg).TransferOp(name, op)
	} else if c, cerr := m.clientFor(cfg); cerr != nil {
		err = cerr
	} else {
		err = c.FileTransferOp(name, op)
		c.Close()
	}
	m.Kick(hostID)
	return err
}

func (m *Manager) ClientOp(hostID, op, mode string) error {
	cfg, ok := m.Store.Get(hostID)
	if !ok {
		return fmt.Errorf("host not found")
	}
	var err error
	if cfg.Demo {
		err = m.mockFor(cfg).ClientOp(op, mode)
	} else if c, cerr := m.clientFor(cfg); cerr != nil {
		err = cerr
	} else {
		switch op {
		case "setRunMode":
			err = c.SetRunMode(mode)
		case "setNetworkMode":
			err = c.SetNetworkMode(mode)
		case "benchmarks":
			err = c.RunBenchmarks()
		default:
			err = fmt.Errorf("invalid client operation")
		}
		c.Close()
	}
	m.Kick(hostID)
	return err
}

func (m *Manager) Attach(hostID, url, auth, name string) error {
	cfg, ok := m.Store.Get(hostID)
	if !ok {
		return fmt.Errorf("host not found")
	}
	var err error
	if cfg.Demo {
		err = m.mockFor(cfg).Attach(url, auth, name)
	} else if c, cerr := m.clientFor(cfg); cerr != nil {
		err = cerr
	} else {
		err = c.ProjectAttach(url, auth, name)
		c.Close()
	}
	m.Kick(hostID)
	return err
}

func (m *Manager) LookupAccount(baseURL, email, pass string) (string, error) {
	return boinc.LookupAccount(baseURL, email, pass)
}

// ClientOpAll applies a client operation to every configured host and returns
// a map of host name -> error for the ones that failed.
func (m *Manager) ClientOpAll(op, mode string) map[string]string {
	failed := map[string]string{}
	for _, h := range m.Store.List() {
		if err := m.ClientOp(h.ID, op, mode); err != nil {
			failed[h.Name] = err.Error()
		}
	}
	return failed
}

// RefreshAll asks the manager to re-poll every configured host right away.
func (m *Manager) RefreshAll() int {
	hosts := m.Store.List()
	for _, h := range hosts {
		m.Kick(h.ID)
	}
	return len(hosts)
}

func (m *Manager) TestHost(cfg HostCfg) (string, error) {
	if cfg.Demo {
		return "8.2.4 (demo)", nil
	}
	c, err := m.clientFor(cfg)
	if err != nil {
		return "", err
	}
	defer c.Close()
	if c.Version == "" {
		return "unknown", nil
	}
	return c.Version, nil
}

func (m *Manager) PrefsGet(hostID string) (map[string]string, error) {
	cfg, ok := m.Store.Get(hostID)
	if !ok {
		return nil, fmt.Errorf("host not found")
	}
	if cfg.Demo {
		return m.mockFor(cfg).GetPrefsOverride(), nil
	}
	c, err := m.clientFor(cfg)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	return c.GetPrefsOverride()
}

func (m *Manager) PrefsSet(hostID string, fields [][2]string) error {
	cfg, ok := m.Store.Get(hostID)
	if !ok {
		return fmt.Errorf("host not found")
	}
	var err error
	if cfg.Demo {
		mp := map[string]string{}
		for _, p := range fields {
			mp[p[0]] = p[1]
		}
		m.mockFor(cfg).SetPrefsOverride(mp)
	} else if c, cerr := m.clientFor(cfg); cerr != nil {
		err = cerr
	} else {
		err = c.SetPrefsOverride(fields)
		c.Close()
	}
	m.Kick(hostID)
	return err
}

func (m *Manager) PrefsClear(hostID string) error {
	return m.PrefsSet(hostID, nil)
}

type StatSeries struct {
	URL   string      `json:"url"`
	Name  string      `json:"name"`
	Daily []StatPoint `json:"daily"`
}

type StatPoint struct {
	Day        string  `json:"day"`
	HostCredit float64 `json:"hostCredit"`
	UserCredit float64 `json:"userCredit"`
}

type XferPoint struct {
	When int64   `json:"when"`
	Up   float64 `json:"up"`
	Down float64 `json:"down"`
}

type DiskProject struct {
	URL       string `json:"url"`
	DiskUsage int64  `json:"diskUsage"`
}

type DiskInfo struct {
	Total    int64         `json:"total"`
	Free     int64         `json:"free"`
	Projects []DiskProject `json:"projects"`
}

const statLayout = "20060102"

// statDay renders a <day> value as YYYYMMDD. BOINC sends unix seconds, older
// Iris builds sent whole days since the epoch.
func statDay(v float64) string {
	sec := int64(v)
	if v < 1e7 {
		sec = int64(v) * 86400
	}
	return time.Unix(sec, 0).UTC().Format(statLayout)
}

func (m *Manager) Stats(hostID string) ([]StatSeries, error) {
	cfg, ok := m.Store.Get(hostID)
	if !ok {
		return nil, fmt.Errorf("host not found")
	}
	nameByURL := map[string]string{}
	if s := m.Snap(hostID); s != nil {
		for _, p := range s.Projects {
			nameByURL[p.URL] = p.Name
		}
	}
	var stats []boinc.ProjectStats
	var err error
	if cfg.Demo {
		stats, err = m.mockFor(cfg).Stats()
	} else if c, cerr := m.clientFor(cfg); cerr != nil {
		err = cerr
	} else {
		stats, err = c.GetStats()
		c.Close()
	}
	if err != nil {
		return nil, err
	}
	var out []StatSeries
	for _, ps := range stats {
		ss := StatSeries{URL: ps.MasterURL, Name: nameByURL[ps.MasterURL]}
		var running float64
		for _, d := range ps.Daily {
			pt := StatPoint{Day: statDay(d.Day.F())}
			if d.Cumulative() {
				// BOINC layout: every entry already holds the running totals.
				pt.HostCredit = d.HostTotalCredit.F()
				pt.UserCredit = d.UserTotalCredit.F()
			} else {
				running += d.TotalCredit.F()
				pt.HostCredit = running
				pt.UserCredit = d.ExpavgCredit.F()
			}
			ss.Daily = append(ss.Daily, pt)
		}
		sort.Slice(ss.Daily, func(i, j int) bool { return ss.Daily[i].Day < ss.Daily[j].Day })
		out = append(out, ss)
	}
	return out, nil
}

func (m *Manager) XferHistory(hostID string) ([]XferPoint, error) {
	cfg, ok := m.Store.Get(hostID)
	if !ok {
		return nil, fmt.Errorf("host not found")
	}
	var dxs []boinc.DailyXfer
	var err error
	if cfg.Demo {
		dxs = m.mockFor(cfg).XferHistory()
	} else {
		c, cerr := m.clientFor(cfg)
		if cerr != nil {
			return nil, cerr
		}
		dxs, err = c.GetDailyXferHistory()
		c.Close()
	}
	if err != nil {
		return nil, err
	}
	out := make([]XferPoint, 0, len(dxs))
	for _, d := range dxs {
		out = append(out, XferPoint{
			When: d.When.I64(),
			Up:   d.Up.F(),
			Down: d.Down.F(),
		})
	}
	return out, nil
}

func (m *Manager) DiskUsage(hostID string) (*DiskInfo, error) {
	cfg, ok := m.Store.Get(hostID)
	if !ok {
		return nil, fmt.Errorf("host not found")
	}
	var du *boinc.DiskUsage
	var err error
	if cfg.Demo {
		du = m.mockFor(cfg).DiskUsage()
	} else {
		c, cerr := m.clientFor(cfg)
		if cerr != nil {
			return nil, cerr
		}
		du, err = c.GetDiskUsage()
		c.Close()
	}
	if err != nil {
		return nil, err
	}
	di := &DiskInfo{
		Total: du.DTotal.I64(),
		Free:  du.DFree.I64(),
	}
	for _, p := range du.Projects {
		di.Projects = append(di.Projects, DiskProject{
			URL:       p.MasterURL,
			DiskUsage: p.DiskUsage.I64(),
		})
	}
	return di, nil
}
