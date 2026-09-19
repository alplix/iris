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
	XMLName   xml.Name   `xml:"client_state"`
	Version   string     `xml:"client_version"`
	HostInfo  HostInfo   `xml:"host_info"`
	Projects  []Project  `xml:"projects>project"`
	Results   []Result   `xml:"results>result"`
	Transfers []Xfer     `xml:"file_transfers>file_transfer"`
	Messages  []Msg      `xml:"msgs>msg"`
	Status    Status     `xml:"cc_status"`
	Stats     []DayStats `xml:"statistics>day"`

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
	Files                     []FileInfo `xml:"file_info"`
	StdOut                    string     `xml:"stdout"`
	StdErr                    string     `xml:"stderr"`
}

type FileInfo struct {
	Name   string  `xml:"name"`
	URL    string  `xml:"url"`
	NBytes float64 `xml:"nbytes"`
	MD5    string  `xml:"md5"`
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
	return xml.Unmarshal(data, s)
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
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Projects {
		if s.Projects[i].MasterURL == url {
			s.Projects[i].SchedRPCPending = 1
			return
		}
	}
}

func (s *State) MarshalState() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return xml.MarshalIndent(s, "", "  ")
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
		Version:  s.Version,
		HostInfo: s.HostInfo,
		Status:   s.Status,
		stateFP:  s.stateFP,
		seqno:    s.seqno,
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
	return cp
}
