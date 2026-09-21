package boinc

import (
	"bytes"
	"encoding/xml"
	"regexp"
	"strconv"
	"strings"
)

var replyRe = regexp.MustCompile(`(?s)^<boinc_gui_rpc_reply>(.*)</boinc_gui_rpc_reply>\s*$`)

func stripWrapper(frame []byte) []byte {
	s := strings.TrimSpace(string(frame))
	if m := replyRe.FindStringSubmatch(s); m != nil {
		s = strings.TrimSpace(m[1])
	}
	return []byte(s)
}

type Num float64

func (n *Num) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	var s string
	if err := d.DecodeElement(&s, &start); err != nil {
		return nil
	}
	s = strings.TrimSpace(s)
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		v = 0
	}
	*n = Num(v)
	return nil
}

func (n Num) F() float64 { return float64(n) }
func (n Num) I() int     { return int(float64(n)) }
func (n Num) I64() int64 { return int64(float64(n)) }
func (n Num) B() bool    { return float64(n) != 0 }

type ClientState struct {
	XMLName        xml.Name     `xml:"client_state"`
	Version        string       `xml:"client_version"`
	HostInfo       HostInfo     `xml:"host_info"`
	Projects       []Project    `xml:"projects>project"`
	Results        []Result     `xml:"results>result"`
	OpenCLGpuProps []OpenCLProp `xml:"opencl_gpu_prop"`
}

type HostInfo struct {
	OSName    string `xml:"os_name"`
	OSVersion string `xml:"os_version"`
	PVendor   string `xml:"p_vendor"`
	PModel    string `xml:"p_model"`
	PNcpus    Num    `xml:"p_ncpus"`
	PFlops    Num    `xml:"p_fpops"`
	MNbytes   Num    `xml:"m_nbytes"`
	DFree     Num    `xml:"d_free"`
	DTotal    Num    `xml:"d_total"`
	HostCPID  string `xml:"host_cpid"`
	BoincVer  string `xml:"boinc_version"`

	Coprocs Coprocs `xml:"coprocs"`
}

type Coprocs struct {
	Count       Num      `xml:"count"`
	Coproc      []Coproc `xml:"coproc"`
	CudaVersion Num      `xml:"cudaVersion"`

	NvidiaDriverVersion string   `xml:"nvidiaDriverVersion"`
	NvidiaDevCount      Num      `xml:"nvidia_dev_count"`
	NvidiaDeviceNames   []string `xml:"nvidia_device_name"`

	AmdDriverVersion string   `xml:"amd_driver_version"`
	AtiDriverVersion string   `xml:"ati_driver_version"`
	AtiDevCount      Num      `xml:"ati_dev_count"`
	AtiDeviceNames   []string `xml:"ati_device_name"`

	IntelGpuDevCount    Num      `xml:"intel_gpu_dev_count"`
	IntelGpuDeviceNames []string `xml:"intel_gpu_device_name"`
	OtherGpuDeviceNames []string `xml:"other_gpu_device_name"`
}

type OpenCLProp struct {
	Vendor    string `xml:"opencl_platform_vendor"`
	Name      string `xml:"opencl_device_name"`
	GlobalMem Num    `xml:"opencl_device_global_mem"`
}

type ClientStateWithOpenCL struct {
	ClientState
	OpenCLGpuProps []OpenCLProp `xml:"opencl_gpu_prop"`
}

type Coproc struct {
	Type   string `xml:"type"`
	IsUsed string `xml:"is_used"`
}

type Project struct {
	Name                string `xml:"name"`
	MasterURL           string `xml:"master_url"`
	ProjectDir          string `xml:"project_dir"`
	Venue               string `xml:"venue"`
	UserName            string `xml:"user_name"`
	TeamName            string `xml:"team_name"`
	UserTotalCredit     Num    `xml:"user_total_credit"`
	UserExpavgCredit    Num    `xml:"user_expavg_credit"`
	HostTotalCredit     Num    `xml:"host_total_credit"`
	HostExpavgCredit    Num    `xml:"host_expavg_credit"`
	ResourceShare       Num    `xml:"resource_share"`
	SuspendedViaGUI     Num    `xml:"suspended_via_gui"`
	DontRequestMoreWork Num    `xml:"dont_request_more_work"`
	SchedRPCPending     Num    `xml:"sched_rpc_pending"`
	Ended               Num    `xml:"ended"`
	LastRPCTime         Num    `xml:"last_rpc_time"`
}

type Result struct {
	Name                      string `xml:"name"`
	WuName                    string `xml:"wu_name"`
	ProjectURL                string `xml:"project_url"`
	State                     Num    `xml:"state"`
	ExitStatus                Num    `xml:"exit_status"`
	FractionDone              Num    `xml:"fraction_done"`
	ElapsedTime               Num    `xml:"elapsed_time"`
	CurrentCPUTime            Num    `xml:"current_cpu_time"`
	CheckpointCPUTime         Num    `xml:"checkpoint_cpu_time"`
	EstimatedCPUTimeRemaining Num    `xml:"estimated_cpu_time_remaining"`
	ReportDeadline            Num    `xml:"report_deadline"`
	WorkingSetSize            Num    `xml:"working_set_size"`
	Resources                 string `xml:"resources"`
	ActiveTask                Num    `xml:"active_task"`
	SuspendedViaGUI           Num    `xml:"suspended_via_gui"`
	ReadyToReport             Num    `xml:"ready_to_report"`
	Slot                      Num    `xml:"slot"`
	VersionNum                Num    `xml:"version_num"`
}

type FileTransfer struct {
	Name         string `xml:"name"`
	ProjectURL   string `xml:"project_url"`
	IsUpload     Num    `xml:"is_upload"`
	Nbytes       Num    `xml:"nbytes"`
	BytesXferred Num    `xml:"bytes_xferred"`
	Paused       Num    `xml:"paused"`
	IsValid      Num    `xml:"is_valid"`
	Status       string `xml:"status"`
}

type CcStatus struct {
	TaskMode    Num `xml:"task_mode"`
	NetworkMode Num `xml:"network_mode"`
}

type Msg struct {
	Seqno   Num    `xml:"seqno"`
	Pri     Num    `xml:"pri"`
	Time    Num    `xml:"time"`
	Body    string `xml:"body"`
	Project string `xml:"project"`
}

type ProjectStats struct {
	MasterURL string      `xml:"master_url"`
	Daily     []DailyStat `xml:"daily_statistics"`
}

type DailyStat struct {
	Day          Num `xml:"day"`
	TotalCredit  Num `xml:"total_credit"`
	ExpavgCredit Num `xml:"expavg_credit"`

	UserTotalCredit  Num `xml:"user_total_credit"`
	UserExpavgCredit Num `xml:"user_expavg_credit"`
	HostTotalCredit  Num `xml:"host_total_credit"`
	HostExpavgCredit Num `xml:"host_expavg_credit"`
}

// Cumulative reports whether the entry carries BOINC's cumulative host/user
// totals (as opposed to the legacy per-day increment layout).
func (d DailyStat) Cumulative() bool {
	return d.HostTotalCredit != 0 || d.UserTotalCredit != 0
}

type DailyXfer struct {
	When Num `xml:"when"`
	Up   Num `xml:"up"`
	Down Num `xml:"down"`
}

type DiskProject struct {
	MasterURL string `xml:"master_url"`
	DiskUsage Num    `xml:"disk_usage"`
}

type DiskUsage struct {
	DTotal   Num           `xml:"d_total"`
	DFree    Num           `xml:"d_free"`
	Projects []DiskProject `xml:"project"`
}

type versionTriple struct {
	Major   Num `xml:"major"`
	Minor   Num `xml:"minor"`
	Release Num `xml:"release"`
}

// VersionsReply covers both the <server_version> element that BOINC-compatible
// clients answer with and the older <versions> form.
type VersionsReply struct {
	Server   versionTriple `xml:"server_version"`
	Versions versionTriple `xml:"versions"`
}

func ParseVersions(frame []byte) (string, error) {
	// The reply's root is <server_version> itself, so its fields land in the
	// embedded triple; the nested forms cover other servers.
	var v struct {
		versionTriple
		VersionsReply
	}
	if err := xml.Unmarshal(stripWrapper(frame), &v); err != nil {
		return "", err
	}
	if v.versionTriple.Major != 0 || v.versionTriple.Minor != 0 || v.versionTriple.Release != 0 {
		v.VersionsReply.Server = v.versionTriple
	}
	t := v.VersionsReply.Server
	if t.Major == 0 && t.Minor == 0 && t.Release == 0 {
		t = v.VersionsReply.Versions
	}
	parts := make([]string, 0, 3)
	for _, n := range []float64{t.Major.F(), t.Minor.F(), t.Release.F()} {
		parts = append(parts, strconv.Itoa(int(n)))
	}
	return strings.Join(parts, "."), nil
}

func ParseInto(frame []byte, out any) error {
	return xml.Unmarshal(stripWrapper(frame), out)
}

// ParseWrapped decodes replies whose top-level element is the list itself
// (<msgs>, <file_transfers>, ...): it re-wraps the frame so that out can
// address the reply with paths such as `msgs>msg`.
func ParseWrapped(frame []byte, out any) error {
	inner := stripWrapper(frame)
	buf := make([]byte, 0, len(inner)+32)
	buf = append(buf, "<reply>"...)
	buf = append(buf, inner...)
	buf = append(buf, "</reply>"...)
	return xml.Unmarshal(buf, out)
}

func ParseOverride(frame []byte) map[string]string {
	out := map[string]string{}
	root := stripWrapper(frame)
	start := bytes.Index(root, []byte("<global_prefs_override>"))
	end := bytes.LastIndex(root, []byte("</global_prefs_override>"))
	if start < 0 || end < 0 {
		return out
	}
	inner := root[start+len("<global_prefs_override>") : end]
	dec := xml.NewDecoder(bytes.NewReader(inner))
	var curKey string
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			curKey = t.Name.Local
		case xml.CharData:
			if curKey != "" {
				out[curKey] = strings.TrimSpace(string(t))
			}
		case xml.EndElement:
			curKey = ""
		}
	}
	return out
}

func EscapeXML(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&apos;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
