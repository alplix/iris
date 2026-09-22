package app

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/alplix/iris/internal/boinc"
	"github.com/alplix/iris/internal/product"
)

type mockProject struct {
	info    ProjectInfo
	pending bool
}

type mockTask struct {
	t        Task
	progress float64
	rate     float64
}

type mockTransfer struct {
	tr   Transfer
	rate float64
}

type Mock struct {
	mu         sync.Mutex
	cfg        HostCfg
	version    string
	projects   []*mockProject
	tasks      []*mockTask
	transfers  []*mockTransfer
	msgs       []boinc.Msg
	seq        int
	runMode    string
	netMode    string
	prefs      map[string]string
	stats      map[string][]boinc.ProjectStats
	xfer       []boinc.DailyXfer
	disk       *boinc.DiskUsage
	lastMsgSeq int
}

func NewMock(cfg HostCfg) *Mock {
	m := &Mock{
		cfg:     cfg,
		version: product.UserAgent(),
		runMode: "auto",
		netMode: "always",
		prefs:   map[string]string{},
	}
	seed := time.Now().UnixNano()
	rnd := rand.New(rand.NewSource(seed))
	type seedProj struct {
		name, url string
		credit    float64
		rac       float64
	}
	sp := []seedProj{
		{"Einstein@Home", "https://einsteinathome.org", 18423311.5, 3212.7},
		{"Rosetta@home", "https://boinc.bakerlab.org/rosetta", 9214776.2, 1876.4},
		{"Milkyway@home", "https://milkyway.cs.rpi.edu/milkyway", 5388210.8, 1102.9},
		{"PrimeGrid", "https://www.primegrid.org", 3141592.6, 842.1},
	}
	for i, s := range sp {
		m.projects = append(m.projects, &mockProject{info: ProjectInfo{
			Name: s.name, URL: s.url, Venue: "work",
			UserName: "Iris Demo", TeamName: "Team Iris",
			UserCredit: s.credit * 1.35, RAC: s.rac * 1.2,
			HostCredit: s.credit, HostRAC: s.rac,
			Share: float64([]int{40, 30, 20, 10}[i%4]),
		}})
	}
	names := []string{"h1_acc", "brca_hydrogen", "mw_native_0p02", "pps_sr2sieve", "h1_acc", "brca_hydrogen"}
	for i := 0; i < 12; i++ {
		url := sp[rnd.Intn(len(sp))].url
		pname := ""
		for _, p := range m.projects {
			if p.info.URL == url {
				pname = p.info.Name
				break
			}
		}
		pr := rnd.Float64() * 0.95
		status := StatusRunning
		switch r := rnd.Float64(); {
		case r < 0.12:
			status = StatusPaused
		case r < 0.18:
			status = StatusError
		case r < 0.26:
			status = StatusQueued
		}
		res := "CPU"
		if rnd.Float64() < 0.45 {
			res = "1 x GeForce RTX 4080 SUPER"
		}
		wss := int64(200000000 + rnd.Float64()*1200000000)
		cpu := pr*3600 + 60
		tk := Task{
			Name: fmt.Sprintf("%s_%d_%d_0", names[i%len(names)], 3500000+rnd.Intn(99999), i),
			Wu:   fmt.Sprintf("wu_%d_%d", 7200000+rnd.Intn(99999), i),
			URL:  url, ProjectName: pname,
			Status: status, Progress: pr,
			Elapsed: cpu * 1.05, CPUTime: cpu,
			ETA: (1 - pr) * 4200, Deadline: time.Now().Add(time.Duration(2+rnd.Intn(96)) * time.Hour).Unix(),
			CPTime: cpu * 0.94, Mem: wss, Resources: res,
			Slot: i % 24, AppVersion: fmt.Sprintf("%d", 704+rnd.Intn(108)),
			Active:    status == StatusRunning || status == StatusPaused,
			Suspended: status == StatusPaused,
		}
		m.tasks = append(m.tasks, &mockTask{t: tk, progress: pr, rate: 0.000008 + rnd.Float64()*0.00002})
	}
	for i := 0; i < 3; i++ {
		// The first transfer is always there; the others come and go.
		if i > 0 && rnd.Float64() < 0.5 {
			continue
		}
		up := rnd.Float64() < 0.4
		total := int64(400000 + rnd.Float64()*900000)
		done := int64(float64(total) * rnd.Float64())
		m.transfers = append(m.transfers, &mockTransfer{
			tr: Transfer{
				Name: fmt.Sprintf("wi_%d_%s_c0_%d", 8800000+i, map[bool]string{true: "upload", false: "download"}[up], i),
				URL:  sp[i%len(sp)].url, ProjectName: sp[i%len(sp)].name,
				Upload: up, Total: total, Done: done, Progress: float64(done) / float64(total),
			},
			rate: 60000 + rnd.Float64()*300000,
		})
	}
	now := time.Now()
	baseMsgs := []struct {
		pri  int
		body string
	}{
		{2, "Scheduler request to einsteinathome.org succeeded"},
		{1, "Running GPU tasks may slow down the display"},
		{3, "Computation for task brca_hydrogen_88 failed"},
		{2, "Work fetch: new work added to project PrimeGrid"},
		{1, "Benchmark results: 4650.12 double precision MIPS"},
	}
	for i := len(baseMsgs) - 1; i >= 0; i-- {
		m.seq++
		m.msgs = append(m.msgs, boinc.Msg{
			Seqno: boinc.Num(m.seq), Pri: boinc.Num(baseMsgs[i].pri),
			Time: boinc.Num(now.Add(-time.Duration(i*7) * time.Minute).Unix()),
			Body: baseMsgs[i].body,
		})
	}
	m.lastMsgSeq = m.seq
	m.buildStats(rnd)
	return m
}

func (m *Mock) buildStats(rnd *rand.Rand) {
	m.stats = map[string][]boinc.ProjectStats{}
	day0 := time.Now().Truncate(24 * time.Hour).Unix()
	for _, p := range m.projects {
		ps := boinc.ProjectStats{MasterURL: p.info.URL}
		var total float64
		for d := 365; d >= 0; d-- {
			gain := p.info.HostRAC * (0.75 + rnd.Float64()*0.5)
			total += gain
			ps.Daily = append(ps.Daily, boinc.DailyStat{
				Day:          boinc.Num((day0 - int64(d)*86400) / 86400),
				TotalCredit:  boinc.Num(p.info.HostCredit - total),
				ExpavgCredit: boinc.Num(gain),
				TasksSuccess: boinc.Num(2 + rnd.Intn(9)),
				TasksError:   boinc.Num(rnd.Intn(2)),
			})
		}
		m.stats[p.info.URL] = append(m.stats[p.info.URL], ps)
	}
	m.xfer = nil
	for d := 29; d >= 0; d-- {
		when := day0 - int64(d)*86400 + 43200
		m.xfer = append(m.xfer, boinc.DailyXfer{
			When: boinc.Num(when),
			Up:   boinc.Num(float64(40+rnd.Intn(220)) * 1048576),
			Down: boinc.Num(float64(90+rnd.Intn(700)) * 1048576),
		})
	}
	var projects []boinc.DiskProject
	for _, p := range m.projects {
		projects = append(projects, boinc.DiskProject{
			MasterURL: p.info.URL,
			DiskUsage: boinc.Num(float64(800000000 + rnd.Int63n(9000000000))),
		})
	}
	m.disk = &boinc.DiskUsage{
		DTotal: boinc.Num(2000398934016), DFree: boinc.Num(987654321098), Projects: projects,
	}
}

func (m *Mock) Snapshot() *Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := &boinc.ClientState{}
	cs := &st.HostInfo.Coprocs
	cs.Count = boinc.Num(1)
	cs.Coproc = []boinc.Coproc{{Type: "NVIDIA", IsUsed: "yes"}}
	cs.CudaVersion = boinc.Num(12080)
	cs.NvidiaDriverVersion = "581.57"
	cs.NvidiaDevCount = boinc.Num(1)
	cs.NvidiaDeviceNames = []string{"GeForce RTX 4080 SUPER"}
	st.OpenCLGpuProps = []boinc.OpenCLProp{{
		Vendor: "NVIDIA Corporation", Name: "NVIDIA GeForce RTX 4080 SUPER",
		GlobalMem: boinc.Num(17175671808),
	}}
	st.HostInfo.OSName = "Microsoft Windows 11"
	st.HostInfo.OSVersion = "Professional Edition, (build 22631)"
	st.HostInfo.PVendor = "AMD"
	st.HostInfo.PModel = "Ryzen 9 7950X 16-core"
	st.HostInfo.PNcpus = boinc.Num(32)
	st.HostInfo.PFlops = boinc.Num(465000000000)
	st.HostInfo.MNbytes = boinc.Num(68719476736)
	st.HostInfo.DFree = boinc.Num(987654321098)
	st.HostInfo.DTotal = boinc.Num(2000398934016)
	st.HostInfo.BoincVer = "8.2.4"
	st.PlatformName = "windows_x86_64"

	for _, t := range m.tasks {
		if t.t.Status != StatusRunning {
			continue
		}
		t.progress += t.rate
		if t.progress >= 1 {
			t.progress = 0
			t.t.Status = StatusReady
			t.t.Ready = true
			t.t.Active = false
			t.t.Progress = 1
			mt := t
			go func() {
				time.Sleep(6 * time.Second)
				m.mu.Lock()
				defer m.mu.Unlock()
				mt.t.Status = StatusRunning
				mt.t.Ready = false
				mt.t.Active = true
			}()
		}
		t.t.Progress = t.progress
		t.t.Elapsed += 4
		t.t.CPUTime += 3.8
		t.t.CPTime = t.t.CPUTime * 0.94
		t.t.ETA = (1 - t.progress) / t.rate
	}
	for _, tr := range m.transfers {
		if tr.tr.Finished || tr.tr.Paused {
			continue
		}
		tr.tr.Done += int64(tr.rate * 4)
		if tr.tr.Done > tr.tr.Total {
			tr.tr.Done = tr.tr.Total
		}
		tr.tr.Progress = float64(tr.tr.Done) / float64(tr.tr.Total)
		if tr.tr.Done >= tr.tr.Total {
			tr.tr.Finished = true
		}
	}

	for _, mp := range m.projects {
		st.Projects = append(st.Projects, boinc.Project{
			Name: mp.info.Name, MasterURL: mp.info.URL, Venue: mp.info.Venue,
			UserName: mp.info.UserName, TeamName: mp.info.TeamName,
			UserTotalCredit: boinc.Num(mp.info.UserCredit), UserExpavgCredit: boinc.Num(mp.info.RAC),
			HostTotalCredit: boinc.Num(mp.info.HostCredit), HostExpavgCredit: boinc.Num(mp.info.HostRAC),
			ResourceShare:       boinc.Num(mp.info.Share),
			SuspendedViaGUI:     boolNum(mp.info.Suspended),
			DontRequestMoreWork: boolNum(mp.info.NoMoreWork),
			SchedRPCPending:     boolNum(mp.info.Pending),
			Ended:               boolNum(mp.info.Ended),
		})
	}

	for _, mt := range m.tasks {
		t := mt.t
		var active, suspended, ready boinc.Num
		switch t.Status {
		case StatusRunning:
			active = boinc.Num(1)
		case StatusPaused:
			active = boinc.Num(1)
			suspended = boinc.Num(1)
		case StatusReady:
			ready = boinc.Num(1)
		}
		st.Results = append(st.Results, boinc.Result{
			Name: t.Name, WuName: t.Wu, ProjectURL: t.URL,
			ExitStatus: boolErr(t.Status), FractionDone: boinc.Num(t.Progress),
			ElapsedTime: boinc.Num(t.Elapsed), CurrentCPUTime: boinc.Num(t.CPUTime),
			CheckpointCPUTime:         boinc.Num(t.CPTime),
			EstimatedCPUTimeRemaining: boinc.Num(t.ETA),
			ReportDeadline:            boinc.Num(float64(t.Deadline)),
			WorkingSetSize:            boinc.Num(float64(t.Mem)),
			Resources:                 t.Resources,
			ActiveTask:                active, SuspendedViaGUI: suspended, ReadyToReport: ready,
			Slot: boinc.Num(float64(t.Slot)), VersionNum: boinc.Num(0),
		})
	}

	var fts []boinc.FileTransfer
	for _, tr := range m.transfers {
		fts = append(fts, boinc.FileTransfer{
			Name: tr.tr.Name, ProjectURL: tr.tr.URL,
			IsUpload: boolNum(tr.tr.Upload), Nbytes: boinc.Num(float64(tr.tr.Total)),
			BytesXferred: boinc.Num(float64(tr.tr.Done)), IsValid: boinc.Num(1),
			Paused: boolNum(tr.tr.Paused),
		})
	}
	cc := &boinc.CcStatus{TaskMode: modeNum(m.runMode), NetworkMode: modeNum(m.netMode)}
	snap := Normalize(m.cfg.ID, true, st, fts, cc, m.msgs, m.version)
	for i := range snap.Projects {
		snap.Projects[i].Pending = m.projects[i].pending
	}
	return snap
}

func boolErr(status TaskStatus) boinc.Num {
	if status == StatusError {
		return boinc.Num(1)
	}
	return boinc.Num(0)
}

func boolNum(b bool) boinc.Num {
	if b {
		return boinc.Num(1)
	}
	return boinc.Num(0)
}

func modeNum(mode string) boinc.Num {
	switch mode {
	case "always":
		return boinc.Num(1)
	case "never":
		return boinc.Num(3)
	default:
		return boinc.Num(2)
	}
}

func (m *Mock) ResultOp(name, op string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tasks {
		if t.t.Name == name {
			switch op {
			case "suspend":
				t.t.Status = StatusPaused
				t.t.Suspended = true
				t.t.Active = true
			case "resume":
				t.t.Status = StatusRunning
				t.t.Suspended = false
				t.t.Active = true
			case "abort":
				m.logLocked(3, "Task "+name+" aborted by user")
			}
			return nil
		}
	}
	return fmt.Errorf("task not found")
}

func (m *Mock) ProjectOp(url, op string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.projects {
		if p.info.URL == url {
			switch op {
			case "suspend":
				p.info.Suspended = true
			case "resume":
				p.info.Suspended = false
			case "nomorework":
				p.info.NoMoreWork = true
			case "allowmorework":
				p.info.NoMoreWork = false
			case "update":
				p.pending = true
				go func(mp *mockProject) {
					time.Sleep(4 * time.Second)
					mp.pending = false
				}(p)
			case "detach":
				m.removeProjectLocked(url)
				return nil
			}
			return nil
		}
	}
	return fmt.Errorf("project not found")
}

func (m *Mock) removeProjectLocked(url string) {
	out := m.projects[:0]
	for _, p := range m.projects {
		if p.info.URL != url {
			out = append(out, p)
		}
	}
	m.projects = out
	outT := m.tasks[:0]
	for _, t := range m.tasks {
		if t.t.URL != url {
			outT = append(outT, t)
		}
	}
	m.tasks = outT
	delete(m.stats, url)
	m.logLocked(2, "Detached from project "+url)
}

func (m *Mock) RemoveProject(url string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeProjectLocked(url)
}

func (m *Mock) TransferOp(name, op string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, tr := range m.transfers {
		if tr.tr.Name == name {
			if op == "retry" {
				tr.tr.Paused = false
			} else if op == "abort" {
				tr.tr.Finished = true
			}
			return nil
		}
	}
	return fmt.Errorf("transfer not found")
}

func (m *Mock) ClientOp(op, mode string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch op {
	case "setRunMode":
		m.runMode = mode
	case "setNetworkMode":
		m.netMode = mode
	case "benchmarks":
		m.logLocked(1, "CPU benchmarks requested by user")
	}
	return nil
}

func (m *Mock) logLocked(pri int, body string) {
	m.seq++
	m.msgs = append(m.msgs, boinc.Msg{
		Seqno: boinc.Num(m.seq), Pri: boinc.Num(pri),
		Time: boinc.Num(time.Now().Unix()), Body: body,
	})
	if len(m.msgs) > 500 {
		m.msgs = m.msgs[len(m.msgs)-500:]
	}
}

func (m *Mock) GetMessages(after int) []boinc.Msg {
	m.mu.Lock()
	defer m.mu.Unlock()
	if after < 0 {
		return append([]boinc.Msg(nil), m.msgs...)
	}
	var out []boinc.Msg
	for _, msg := range m.msgs {
		if int(msg.Seqno.F()) > after {
			out = append(out, msg)
		}
	}
	return out
}

func (m *Mock) LastSeq() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastMsgSeq
}

func (m *Mock) GetPrefsOverride() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]string{}
	for k, v := range m.prefs {
		out[k] = v
	}
	return out
}

func (m *Mock) SetPrefsOverride(fields map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, v := range fields {
		m.prefs[k] = v
	}
}

func (m *Mock) Stats() ([]boinc.ProjectStats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []boinc.ProjectStats
	for _, list := range m.stats {
		out = append(out, list...)
	}
	return out, nil
}

func (m *Mock) XferHistory() []boinc.DailyXfer {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]boinc.DailyXfer(nil), m.xfer...)
}

func (m *Mock) DiskUsage() *boinc.DiskUsage {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *m.disk
	return &cp
}

func (m *Mock) Attach(url, auth, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.projects {
		if p.info.URL == url {
			return fmt.Errorf("already attached to this project")
		}
	}
	displayName := name
	if displayName == "" {
		displayName = hostLabel(url)
	}
	m.projects = append(m.projects, &mockProject{info: ProjectInfo{
		Name: displayName, URL: url, Share: 100,
		UserName: "Iris Demo", TeamName: "Team Iris",
	}})
	m.logLocked(2, "Attaching to project "+url)
	return nil
}

func hostLabel(rawURL string) string {
	s := rawURL
	for _, pre := range []string{"https://", "http://"} {
		if strings.HasPrefix(s, pre) {
			s = s[len(pre):]
			break
		}
	}
	if i := strings.Index(s, "/"); i >= 0 {
		s = s[:i]
	}
	return s
}
