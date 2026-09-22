package assistant

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alplix/iris/internal/app"
)

const (
	maxDigestHosts    = 6
	maxDigestProjects = 6
	statsFetchBudget  = 5 * time.Second
	statsHostLimit    = 3 // fetch daily task/credit history for at most this many hosts
)

// buildDigest summarises the fleet for the model: enough to answer "how many
// Einstein tasks do I finish per day" and similar questions accurately,
// without ever including a password, RPC key or account authenticator —
// those never leave the manager's own store.
func buildDigest(mgr *app.Manager) string {
	hosts := mgr.Store.List()
	if len(hosts) == 0 {
		return "IRIS FLEET: no servers configured yet."
	}
	sort.Slice(hosts, func(i, j int) bool { return hosts[i].Name < hosts[j].Name })

	taskStats := fetchRecentTaskStats(mgr, hosts)

	var b strings.Builder
	b.WriteString("IRIS FLEET SUMMARY\n")
	shown, extra := hosts, 0
	if len(shown) > maxDigestHosts {
		extra = len(shown) - maxDigestHosts
		shown = shown[:maxDigestHosts]
	}
	for _, h := range shown {
		writeHostLine(&b, h, mgr.Snap(h.ID), taskStats[h.ID])
	}
	if extra > 0 {
		fmt.Fprintf(&b, "(+%d more servers not shown)\n", extra)
	}
	return b.String()
}

func writeHostLine(b *strings.Builder, h app.HostCfg, snap *app.Snapshot, tasks map[string]projectTaskStat) {
	kind := "real"
	if h.Demo {
		kind = "demo"
	}
	if snap == nil || !snap.Online {
		fmt.Fprintf(b, "Host %q (%s): offline\n", h.Name, kind)
		return
	}
	fmt.Fprintf(b, "Host %q (%s) v%s: %d running, %d paused, %d queued, %d errors, %d downloading, %d uploading, run=%s net=%s, RAC %.0f, credit %.0f\n",
		h.Name, kind, orDash(snap.Version), snap.Totals.Running, snap.Totals.Paused, snap.Totals.Queued, snap.Totals.Errors,
		snap.Totals.Downloads, snap.Totals.Uploads, orDash(snap.TaskMode), orDash(snap.NetMode), snap.Totals.RAC, snap.Totals.Credit)
	writeHardwareLine(b, snap.HostInfo)
	writeRecentIssueLine(b, snap.Messages)

	projs := append([]app.ProjectInfo(nil), snap.Projects...)
	sort.Slice(projs, func(i, j int) bool { return projs[i].HostRAC > projs[j].HostRAC })
	extra := 0
	if len(projs) > maxDigestProjects {
		extra = len(projs) - maxDigestProjects
		projs = projs[:maxDigestProjects]
	}
	for _, p := range projs {
		var flags []string
		if p.Suspended {
			flags = append(flags, "suspended")
		}
		if p.NoMoreWork {
			flags = append(flags, "no-more-work")
		}
		line := fmt.Sprintf("  - %q: RAC %.0f, credit %.0f", p.Name, p.HostRAC, p.HostCredit)
		if len(flags) > 0 {
			line += " [" + strings.Join(flags, ",") + "]"
		}
		if ts, ok := tasks[p.URL]; ok {
			line += fmt.Sprintf(", tasks completed today=%d last7days=%d errors7days=%d", ts.today, ts.week, ts.weekErr)
		}
		b.WriteString(line + "\n")
	}
	if extra > 0 {
		fmt.Fprintf(b, "  (+%d more projects on this host)\n", extra)
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// writeHardwareLine adds one compact line of the host's real hardware, so
// the assistant can answer "what CPU/GPU/RAM/disk does this machine have"
// without guessing — the same facts already shown in Settings → Hardware.
func writeHardwareLine(b *strings.Builder, hw app.HostSpec) {
	if hw.CPU == "" && hw.Memory == 0 && len(hw.GPUs) == 0 {
		return // offline snapshot, or a client too old to report it
	}
	fmt.Fprintf(b, "  HW: %s, %d cores, RAM %s, disk %s free of %s",
		orDash(hw.CPU), hw.Cores, gib(hw.Memory), gib(hw.DiskFree), gib(hw.DiskTotal))
	if len(hw.GPUs) > 0 {
		var names []string
		for _, g := range hw.GPUs {
			names = append(names, fmt.Sprintf("%s (%s)", strings.Join(g.Names, "/"), gib(g.VRAM)))
		}
		fmt.Fprintf(b, "; GPU: %s", strings.Join(names, ", "))
	}
	b.WriteString("\n")
}

func gib(bytes int64) string {
	return fmt.Sprintf("%.0fGB", float64(bytes)/(1<<30))
}

// writeRecentIssueLine surfaces the single most recent high-priority client
// message (BOINC's MSG_INTERNAL_ERROR tier), if there is one, so someone can
// ask the assistant to help with "what's wrong" instead of digging through
// the Messages page themselves.
func writeRecentIssueLine(b *strings.Builder, msgs []app.MsgLine) {
	const errorPriority = 3
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Pri >= errorPriority {
			body := msgs[i].Body
			if r := []rune(body); len(r) > 160 {
				body = string(r[:160]) + "…"
			}
			fmt.Fprintf(b, "  Recent issue: %s\n", body)
			return
		}
	}
}

type projectTaskStat struct {
	today, week, weekErr int
}

// fetchRecentTaskStats gets each project's finished-task counts (today and
// the last 7 days) for up to statsHostLimit hosts, each running concurrently
// and bounded by one shared budget, so one slow or offline remote host never
// stalls the whole chat turn.
func fetchRecentTaskStats(mgr *app.Manager, hosts []app.HostCfg) map[string]map[string]projectTaskStat {
	if len(hosts) > statsHostLimit {
		hosts = hosts[:statsHostLimit]
	}
	out := map[string]map[string]projectTaskStat{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, h := range hosts {
		wg.Add(1)
		go func(h app.HostCfg) {
			defer wg.Done()
			m := projectTaskStatsFor(mgr, h.ID)
			if m == nil {
				return
			}
			mu.Lock()
			out[h.ID] = m
			mu.Unlock()
		}(h)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(statsFetchBudget):
	}
	mu.Lock()
	defer mu.Unlock()
	// Return a shallow copy so the caller never races with a goroutine that
	// is still writing after the deadline above.
	cp := make(map[string]map[string]projectTaskStat, len(out))
	for k, v := range out {
		cp[k] = v
	}
	return cp
}

func projectTaskStatsFor(mgr *app.Manager, hostID string) map[string]projectTaskStat {
	series, err := mgr.Stats(hostID)
	if err != nil {
		return nil
	}
	today := time.Now().UTC().Format(statDayLayout)
	cutoff := time.Now().UTC().AddDate(0, 0, -7).Format(statDayLayout)
	m := map[string]projectTaskStat{}
	for _, ss := range series {
		var st projectTaskStat
		for _, pt := range ss.Daily {
			// StatPoint.Day is "YYYYMMDD": fixed-width zero-padded digits sort
			// (and compare) the same lexicographically as chronologically.
			if pt.Day >= cutoff {
				st.week += pt.TasksSuccess
				st.weekErr += pt.TasksError
			}
			if pt.Day == today {
				st.today += pt.TasksSuccess
			}
		}
		m[ss.URL] = st
	}
	return m
}

const statDayLayout = "20060102"
