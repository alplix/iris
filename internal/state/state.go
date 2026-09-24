package state

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alplix/iris/internal/product"
)

type State struct {
	XMLName xml.Name `xml:"client_state"`
	Version string   `xml:"client_version"`
	// PlatformName is the BOINC platform this client asks projects for work as.
	PlatformName string     `xml:"platform_name"`
	HostInfo     HostInfo   `xml:"host_info"`
	Projects     []Project  `xml:"projects>project"`
	Results      []Result   `xml:"results>result"`
	Transfers    []Xfer     `xml:"file_transfers>file_transfer"`
	Messages     []Msg      `xml:"msgs>msg"`
	Status       Status     `xml:"cc_status"`
	Stats        []DayStats `xml:"statistics>day"`

	OpenCLGpuProps []OpenCLProp `xml:"opencl_gpu_prop"`
	Credits        []CreditDay  `xml:"credit_history>day"`
	Xfers          []DayXfer    `xml:"daily_xfers>dx"`
	TaskDays       []TaskDay    `xml:"task_history>day"`

	mu      sync.RWMutex
	stateFP string
	seqno   int
}

type HostInfo struct {
	XMLName   xml.Name `xml:"host_info"`
	OSName    string   `xml:"os_name"`
	OSVersion string   `xml:"os_version"`
	PVendor   string   `xml:"p_vendor"`
	PModel    string   `xml:"p_model"`
	PNcpus    float64  `xml:"p_ncpus"`
	PFlops    float64  `xml:"p_fpops"`
	MNbytes   float64  `xml:"m_nbytes"`
	DFree     float64  `xml:"d_free"`
	DTotal    float64  `xml:"d_total"`
	HostCPID  string   `xml:"host_cpid"`
	CamVer    string   `xml:"iris_version"`
	GPUs      []string `xml:"gpu>name"`
	Coprocs   Coprocs  `xml:"coprocs"`
}

// Coprocs mirrors the GPU section of a BOINC host_info so managers can list
// the devices and their vendors.
type Coprocs struct {
	Count               float64  `xml:"count"`
	CudaVersion         float64  `xml:"cudaVersion,omitempty"`
	NvidiaDriverVersion string   `xml:"nvidiaDriverVersion,omitempty"`
	NvidiaDevCount      float64  `xml:"nvidia_dev_count,omitempty"`
	NvidiaCCMajor       int      `xml:"nvidia_cc_major,omitempty"`
	NvidiaCCMinor       int      `xml:"nvidia_cc_minor,omitempty"`
	NvidiaDeviceNames   []string `xml:"nvidia_device_name"`
	AmdDriverVersion    string   `xml:"amd_driver_version,omitempty"`
	AtiDevCount         float64  `xml:"ati_dev_count,omitempty"`
	AtiDeviceNames      []string `xml:"ati_device_name"`
	IntelGpuDevCount    float64  `xml:"intel_gpu_dev_count,omitempty"`
	IntelGpuDeviceNames []string `xml:"intel_gpu_device_name"`
	OtherGpuDeviceNames []string `xml:"other_gpu_device_name"`
}

type OpenCLProp struct {
	Vendor    string  `xml:"opencl_platform_vendor"`
	Name      string  `xml:"opencl_device_name"`
	GlobalMem float64 `xml:"opencl_device_global_mem"`
}

// CreditDay is one project's credit totals as of a given day (unix days).
type CreditDay struct {
	URL        string  `xml:"url"`
	Day        int64   `xml:"d"`
	UserTotal  float64 `xml:"ut"`
	UserExpavg float64 `xml:"ue"`
	HostTotal  float64 `xml:"ht"`
	HostExpavg float64 `xml:"he"`
}

// DayXfer is the number of bytes moved on one day (unix days), in BOINC's
// daily_xfers layout.
type DayXfer struct {
	When int64   `xml:"when"`
	Up   float64 `xml:"up"`
	Down float64 `xml:"down"`
}

// TaskDay is one project's finished-task counts for a given day (unix days).
// It is recorded independently of CreditDay: task completion (worker) and
// credit (scheduler RPC) happen on different, unrelated schedules.
type TaskDay struct {
	URL     string  `xml:"url"`
	Day     int64   `xml:"d"`
	Success int     `xml:"s"`
	Error   int     `xml:"e"`
	CPUTime float64 `xml:"c"`
}

type Project struct {
	Name                string  `xml:"name"`
	MasterURL           string  `xml:"master_url"`
	ProjectDir          string  `xml:"project_dir"`
	Venue               string  `xml:"venue"`
	UserName            string  `xml:"user_name"`
	TeamName            string  `xml:"team_name"`
	UserTotalCredit     float64 `xml:"user_total_credit"`
	UserExpavgCredit    float64 `xml:"user_expavg_credit"`
	HostTotalCredit     float64 `xml:"host_total_credit"`
	HostExpavgCredit    float64 `xml:"host_expavg_credit"`
	ResourceShare       float64 `xml:"resource_share"`
	SuspendedViaGUI     int     `xml:"suspended_via_gui"`
	DontRequestMoreWork int     `xml:"dont_request_more_work"`
	SchedRPCPending     int     `xml:"sched_rpc_pending"`
	Ended               int     `xml:"ended"`
	LastRPCTime         float64 `xml:"last_rpc_time"`
	Authenticator       string  `xml:"authenticator"`
	// HostID is the id the project's server assigned this computer and
	// RPCSeqno how many scheduler contacts were made; both go back to the
	// server on every request.
	HostID   int `xml:"hostid"`
	RPCSeqno int `xml:"rpc_seqno"`
}

type Result struct {
	Name                      string     `xml:"name"`
	WuName                    string     `xml:"wu_name"`
	ProjectURL                string     `xml:"project_url"`
	State                     int        `xml:"state"`
	ExitStatus                int        `xml:"exit_status"`
	FractionDone              float64    `xml:"fraction_done"`
	ElapsedTime               float64    `xml:"elapsed_time"`
	CurrentCPUTime            float64    `xml:"current_cpu_time"`
	EstimatedCPUTimeRemaining float64    `xml:"estimated_cpu_time_remaining"`
	ReportDeadline            float64    `xml:"report_deadline"`
	WorkingSetSize            float64    `xml:"working_set_size"`
	Resources                 string     `xml:"resources"`
	ActiveTask                int        `xml:"active_task"`
	SuspendedViaGUI           int        `xml:"suspended_via_gui"`
	ReadyToReport             int        `xml:"ready_to_report"`
	Slot                      int        `xml:"slot"`
	SlotPath                  string     `xml:"slot_path"`
	VersionNum                int        `xml:"version_num"`
	CmdLine                   string     `xml:"cmd_line"`
	AppVersionNum             int        `xml:"app_version_num"`
	AppName                   string     `xml:"app_name"`
	Platform                  string     `xml:"platform"`
	PlanClass                 string     `xml:"plan_class"`
	Files                     []FileInfo `xml:"file_info"`
	// Outputs are the files the task must produce and upload before it can be
	// reported (from the reply's generated_locally file_infos).
	Outputs []OutputFile `xml:"output_file"`
	// EstimatedRuntime is the expected seconds of work on this host, used to
	// show progress for applications that do not report any.
	EstimatedRuntime float64 `xml:"estimated_runtime,omitempty"`
	StdOut           string  `xml:"stdout"`
	StdErr           string  `xml:"stderr"`
}

type FileInfo struct {
	Name   string  `xml:"name"`
	URL    string  `xml:"url"`
	NBytes float64 `xml:"nbytes"`
	MD5    string  `xml:"md5"`
	// MainProgram marks the downloaded file (from a project's app_version)
	// that is the real executable to launch for this task.
	MainProgram bool `xml:"main_program,omitempty"`
	// OpenName is the logical name the application opens the file by; the
	// downloaded file is also made available under it in the task's slot.
	OpenName string `xml:"open_name,omitempty"`
	// LocalPath, when set, is where the file already is (the person's own
	// application named in app_info.xml); it is copied, never downloaded.
	LocalPath string `xml:"local_path,omitempty"`
}

// OutputFile is one file a task produces. Name is the physical name the
// project's upload certificate was signed for, OpenName the name the
// application writes it under in its slot directory.
type OutputFile struct {
	Name      string   `xml:"name"`
	OpenName  string   `xml:"open_name,omitempty"`
	URLs      []string `xml:"url"`
	MaxNBytes float64  `xml:"max_nbytes"`
	Signature string   `xml:"xml_signature"`
	Optional  bool     `xml:"optional,omitempty"`
	// Filled in once the task has finished and the file exists.
	Present  bool    `xml:"present,omitempty"`
	NBytes   float64 `xml:"nbytes,omitempty"`
	MD5      string  `xml:"md5_cksum,omitempty"`
	Uploaded bool    `xml:"uploaded,omitempty"`
}

func (r *Result) IsGPU() bool {
	res := strings.ToLower(r.Resources)
	for _, kw := range []string{"cuda", "opencl", "gpu", "nvidia", "amd", "radeon", "geforce"} {
		if strings.Contains(res, kw) {
			return true
		}
	}
	return false
}

type Xfer struct {
	Name         string  `xml:"name"`
	ProjectURL   string  `xml:"project_url"`
	IsUpload     int     `xml:"is_upload"`
	Nbytes       float64 `xml:"nbytes"`
	BytesXferred float64 `xml:"bytes_xferred"`
	Paused       int     `xml:"paused"`
	IsValid      int     `xml:"is_valid"`
}

type Msg struct {
	Seqno   int     `xml:"seqno"`
	Pri     int     `xml:"pri"`
	Time    float64 `xml:"time"`
	Body    string  `xml:"body"`
	Project string  `xml:"project"`
}

type Status struct {
	TaskMode    int   `xml:"task_mode"`
	NetworkMode int   `xml:"network_mode"`
	DiskUsage   int64 `xml:"disk_usage"`
	DiskQuota   int64 `xml:"disk_quota"`
}

type DayStats struct {
	Date         string  `xml:"date"`
	Tasks        int     `xml:"tasks"`
	TasksSuccess int     `xml:"tasks_success"`
	TasksError   int     `xml:"tasks_error"`
	TotalCPU     float64 `xml:"total_cpu"`
	TotalGPU     float64 `xml:"total_gpu"`
	CreditEarned float64 `xml:"credit_earned"`
}

func New(dataDir string) *State {
	return &State{
		Version: product.UserAgent(),
		stateFP: filepath.Join(dataDir, "client_state.xml"),
		Status: Status{
			TaskMode:    1,
			NetworkMode: 1,
		},
	}
}

func (s *State) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.stateFP)
	if err != nil {
		return nil
	}
	if err := xml.Unmarshal(data, s); err != nil {
		return err
	}
	// Transfers are live objects; anything persisted belongs to a previous run.
	s.Transfers = nil
	// Message numbers must keep counting from where the last run stopped, or
	// every new message would look older than what a manager has already seen
	// and never be shown.
	for _, m := range s.Messages {
		if m.Seqno > s.seqno {
			s.seqno = m.Seqno
		}
	}
	return nil
}

func (s *State) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *State) saveLocked() error {
	data, err := xml.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	var buf strings.Builder
	buf.WriteString(xml.Header)
	buf.Write(data)
	buf.WriteString("\n")
	return os.WriteFile(s.stateFP, []byte(buf.String()), 0o644)
}

func (s *State) RLock()   { s.mu.RLock() }
func (s *State) RUnlock() { s.mu.RUnlock() }
func (s *State) Lock()    { s.mu.Lock() }
func (s *State) Unlock()  { s.mu.Unlock() }

func (s *State) AddMessage(body, project string, pri int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seqno++
	s.Messages = append(s.Messages, Msg{
		Seqno:   s.seqno,
		Pri:     pri,
		Time:    float64(time.Now().Unix()),
		Body:    body,
		Project: project,
	})
	if len(s.Messages) > 1000 {
		s.Messages = s.Messages[len(s.Messages)-500:]
	}
}

func (s *State) AddProject(p Project) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.Projects {
		if existing.MasterURL == p.MasterURL {
			s.Projects[i] = p
			return
		}
	}
	s.Projects = append(s.Projects, p)
}

func (s *State) RemoveProject(url string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, p := range s.Projects {
		if p.MasterURL == url {
			s.Projects = append(s.Projects[:i], s.Projects[i+1:]...)
			return
		}
	}
}

func (s *State) AddResult(r Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.Results {
		if existing.Name == r.Name {
			s.Results[i] = r
			return
		}
	}
	s.Results = append(s.Results, r)
}

// SetOutputs stores what the finished task actually produced (present/size/MD5
// per output) and, when nothing is left to upload, marks it ready to report.
func (s *State) SetOutputs(name string, outs []OutputFile) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Results {
		if s.Results[i].Name == name {
			s.Results[i].Outputs = outs
			return
		}
	}
}

// MarkOutputUploaded records one output as uploaded; when every present
// output is, the result becomes ready to report. It returns true then.
func (s *State) MarkOutputUploaded(name, file string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Results {
		r := &s.Results[i]
		if r.Name != name {
			continue
		}
		all := true
		for j := range r.Outputs {
			if r.Outputs[j].Name == file {
				r.Outputs[j].Uploaded = true
			}
			if r.Outputs[j].Present && !r.Outputs[j].Uploaded {
				all = false
			}
		}
		if all {
			r.ReadyToReport = 1
		}
		return all
	}
	return false
}

// RetryDownloadFailures puts tasks that only failed because their files could
// not be downloaded (and were not reported yet) back in the queue. It returns
// how many changed. Used at start-up to recover work lost to a since-fixed
// slot-sharing bug.
func (s *State) RetryDownloadFailures(exitStatus int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for i := range s.Results {
		r := &s.Results[i]
		if r.State == 5 && r.ExitStatus == exitStatus && r.ReadyToReport == 1 {
			r.State, r.ExitStatus, r.ReadyToReport, r.ActiveTask = 0, 0, 0, 0
			n++
		}
	}
	return n
}

func (s *State) RemoveResult(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.Results {
		if r.Name == name {
			s.Results = append(s.Results[:i], s.Results[i+1:]...)
			return
		}
	}
}

func (s *State) GetMessages(after int) []Msg {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if after < 0 {
		return s.Messages
	}
	var out []Msg
	for _, m := range s.Messages {
		if m.Seqno > after {
			out = append(out, m)
		}
	}
	return out
}

func (s *State) SetTaskMode(mode int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Status.TaskMode = mode
}

func (s *State) SetNetworkMode(mode int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Status.NetworkMode = mode
}

func (s *State) SetSuspended(name string, suspended bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	val := 0
	if suspended {
		val = 1
	}
	for i, r := range s.Results {
		if r.Name == name {
			s.Results[i].SuspendedViaGUI = val
		}
	}
}

func (s *State) SetProjectSuspended(url string, suspended bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	val := 0
	if suspended {
		val = 1
	}
	for i := range s.Projects {
		if s.Projects[i].MasterURL == url {
			s.Projects[i].SuspendedViaGUI = val
			return
		}
	}
}

func (s *State) SetProjectUpdate(url string) {
	s.SetProjectSchedPending(url, true)
}

// SetProjectSchedPending marks whether a project is waiting for its next
// scheduler contact. SetProjectUpdate sets it true when the person asks for
// an update; the scheduler engine clears it back to false once it actually
// attempts that contact (see scheduler.Engine.doRPC) — without this, a
// project that was ever updated stayed marked "updating" in the UI forever,
// even after the request had long since succeeded or failed.
func (s *State) SetProjectSchedPending(url string, pending bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	val := 0
	if pending {
		val = 1
	}
	for i := range s.Projects {
		if s.Projects[i].MasterURL == url {
			s.Projects[i].SchedRPCPending = val
			return
		}
	}
}

// SetProjectRPCState records the host id a project's server assigned this
// computer and the count of scheduler contacts.
func (s *State) SetProjectRPCState(url string, hostID, rpcSeqno int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Projects {
		if s.Projects[i].MasterURL == url {
			s.Projects[i].HostID = hostID
			s.Projects[i].RPCSeqno = rpcSeqno
			return
		}
	}
}

// SetProjectName gives a project its name, but only if it has none: a
// project attached with just its URL learns its real name from the
// scheduler's first reply, and a name the person chose is never overwritten.
// SetProjectNoMoreWork switches a project's "don't request new tasks" flag.
func (s *State) SetProjectNoMoreWork(url string, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Projects {
		if s.Projects[i].MasterURL == url {
			if on {
				s.Projects[i].DontRequestMoreWork = 1
			} else {
				s.Projects[i].DontRequestMoreWork = 0
			}
			return
		}
	}
}

// SetProjectDir records a project's folder (after it was renamed).
func (s *State) SetProjectDir(url, dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Projects {
		if s.Projects[i].MasterURL == url {
			s.Projects[i].ProjectDir = dir
			return
		}
	}
}

func (s *State) SetProjectName(url, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Projects {
		if s.Projects[i].MasterURL == url && strings.TrimSpace(s.Projects[i].Name) == "" {
			s.Projects[i].Name = name
			return
		}
	}
}

func (s *State) MarshalState() ([]byte, error) {
	cp := s.Snapshot()
	cp.Credits = nil
	cp.Xfers = nil
	cp.TaskDays = nil
	return xml.MarshalIndent(cp, "", "  ")
}

func (s *State) GetNetworkMode() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Status.NetworkMode
}

func (s *State) GetTaskMode() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Status.TaskMode
}

func (s *State) GetDiskQuota() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Status.DiskQuota
}

func (s *State) GetDiskUsage() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Status.DiskUsage
}

func (s *State) SetDiskUsage(v int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Status.DiskUsage = v
}

func (s *State) UpdateStats(success bool, cpuTime, gpuTime, credit float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	today := time.Now().Format("2006-01-02")
	for i := range s.Stats {
		if s.Stats[i].Date == today {
			s.Stats[i].Tasks++
			if success {
				s.Stats[i].TasksSuccess++
			} else {
				s.Stats[i].TasksError++
			}
			s.Stats[i].TotalCPU += cpuTime
			s.Stats[i].TotalGPU += gpuTime
			s.Stats[i].CreditEarned += credit
			return
		}
	}
	ds := DayStats{Date: today, Tasks: 1}
	if success {
		ds.TasksSuccess = 1
	} else {
		ds.TasksError = 1
	}
	ds.TotalCPU = cpuTime
	ds.TotalGPU = gpuTime
	ds.CreditEarned = credit
	s.Stats = append(s.Stats, ds)
	if len(s.Stats) > 365 {
		s.Stats = s.Stats[len(s.Stats)-365:]
	}
}

func (s *State) GetProjectByURL(url string) *Project {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.Projects {
		if s.Projects[i].MasterURL == url {
			return &s.Projects[i]
		}
	}
	return nil
}

func (s *State) Snapshot() *State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := &State{
		Version:      s.Version,
		PlatformName: s.PlatformName,
		HostInfo:     s.HostInfo,
		Status:       s.Status,
		stateFP:      s.stateFP,
		seqno:        s.seqno,
	}
	cp.Projects = make([]Project, len(s.Projects))
	copy(cp.Projects, s.Projects)
	cp.Results = make([]Result, len(s.Results))
	copy(cp.Results, s.Results)
	cp.Transfers = make([]Xfer, len(s.Transfers))
	copy(cp.Transfers, s.Transfers)
	cp.Messages = make([]Msg, len(s.Messages))
	copy(cp.Messages, s.Messages)
	cp.Stats = make([]DayStats, len(s.Stats))
	copy(cp.Stats, s.Stats)
	cp.OpenCLGpuProps = append([]OpenCLProp(nil), s.OpenCLGpuProps...)
	cp.Credits = append([]CreditDay(nil), s.Credits...)
	cp.Xfers = append([]DayXfer(nil), s.Xfers...)
	cp.TaskDays = append([]TaskDay(nil), s.TaskDays...)
	return cp
}

const (
	maxCreditDays = 365
	maxXferDays   = 365
)

func unixDay(t time.Time) int64 { return t.Unix() / 86400 }

// UpdateProjectCredit stores the credit figures a project scheduler reported
// and records them in the per-day history the Statistics page charts.
func (s *State) UpdateProjectCredit(url string, userTotal, userExpavg, hostTotal, hostExpavg float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Projects {
		if s.Projects[i].MasterURL == url {
			s.Projects[i].UserTotalCredit = userTotal
			s.Projects[i].UserExpavgCredit = userExpavg
			s.Projects[i].HostTotalCredit = hostTotal
			s.Projects[i].HostExpavgCredit = hostExpavg
			break
		}
	}
	day := unixDay(time.Now())
	entry := CreditDay{URL: url, Day: day, UserTotal: userTotal, UserExpavg: userExpavg, HostTotal: hostTotal, HostExpavg: hostExpavg}
	for i := range s.Credits {
		if s.Credits[i].URL == url && s.Credits[i].Day == day {
			s.Credits[i] = entry
			return
		}
	}
	s.Credits = append(s.Credits, entry)
	cutoff := day - maxCreditDays
	kept := s.Credits[:0]
	for _, c := range s.Credits {
		if c.Day > cutoff {
			kept = append(kept, c)
		}
	}
	s.Credits = kept
}

// RecordTaskDay counts one finished task against its project's daily total —
// the data behind "how many Project X tasks did I complete today/this week".
func (s *State) RecordTaskDay(projectURL string, success bool, cpuTime float64) {
	if projectURL == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	day := unixDay(time.Now())
	for i := range s.TaskDays {
		if s.TaskDays[i].URL == projectURL && s.TaskDays[i].Day == day {
			if success {
				s.TaskDays[i].Success++
			} else {
				s.TaskDays[i].Error++
			}
			s.TaskDays[i].CPUTime += cpuTime
			return
		}
	}
	td := TaskDay{URL: projectURL, Day: day, CPUTime: cpuTime}
	if success {
		td.Success = 1
	} else {
		td.Error = 1
	}
	s.TaskDays = append(s.TaskDays, td)
	cutoff := day - maxCreditDays
	kept := s.TaskDays[:0]
	for _, t := range s.TaskDays {
		if t.Day > cutoff {
			kept = append(kept, t)
		}
	}
	s.TaskDays = kept
}

// AddXfer accounts n transferred bytes to today's totals.
func (s *State) AddXfer(upload bool, n int64) {
	if n <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	day := unixDay(time.Now())
	idx := -1
	for i := range s.Xfers {
		if s.Xfers[i].When == day {
			idx = i
			break
		}
	}
	if idx < 0 {
		s.Xfers = append(s.Xfers, DayXfer{When: day})
		idx = len(s.Xfers) - 1
		if len(s.Xfers) > maxXferDays {
			s.Xfers = s.Xfers[len(s.Xfers)-maxXferDays:]
			idx = len(s.Xfers) - 1
		}
	}
	if upload {
		s.Xfers[idx].Up += float64(n)
	} else {
		s.Xfers[idx].Down += float64(n)
	}
}

// BeginTransfer registers a live file transfer, replacing an earlier (failed)
// one with the same name.
func (s *State) BeginTransfer(x Xfer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Transfers {
		if s.Transfers[i].Name == x.Name {
			s.Transfers[i] = x
			return
		}
	}
	s.Transfers = append(s.Transfers, x)
}

func (s *State) SetTransferProgress(name string, done, total float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Transfers {
		if s.Transfers[i].Name == name {
			s.Transfers[i].BytesXferred = done
			if total > 0 {
				s.Transfers[i].Nbytes = total
			}
			return
		}
	}
}

// EndTransfer removes a finished transfer. A failed one is kept in a paused
// state so it can be retried from the manager.
func (s *State) EndTransfer(name string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Transfers {
		if s.Transfers[i].Name != name {
			continue
		}
		if ok {
			s.Transfers = append(s.Transfers[:i], s.Transfers[i+1:]...)
		} else {
			s.Transfers[i].Paused = 1
		}
		return
	}
}

// TransferFailed reports whether name is a kept, failed transfer.
func (s *State) TransferFailed(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.Transfers {
		if t.Name == name {
			return t.Paused == 1
		}
	}
	return false
}

func (s *State) RemoveTransfer(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Transfers {
		if s.Transfers[i].Name == name {
			s.Transfers = append(s.Transfers[:i], s.Transfers[i+1:]...)
			return
		}
	}
}

// ResetResultsWithFile puts errored results that reference the named file back
// into the queue so the worker tries them again. It returns how many changed.
func (s *State) ResetResultsWithFile(name string, errState, newState int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for i := range s.Results {
		r := &s.Results[i]
		if r.State != errState {
			continue
		}
		for _, f := range r.Files {
			if f.Name == name {
				r.State = newState
				r.ExitStatus = 0
				r.ReadyToReport = 0
				r.ActiveTask = 0
				n++
				break
			}
		}
	}
	return n
}

func (s *State) SetPFlops(v float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.HostInfo.PFlops = v
}
