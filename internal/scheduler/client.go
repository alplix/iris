package scheduler

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/alplix/iris/internal/product"
)

type Client struct {
	projectURL   string
	authToken    string
	schedulerURL string
	httpClient   *http.Client
}

type Request struct {
	// MsgsFromHost are pending trickle-up messages (see trickle.go).
	MsgsFromHost  []MsgFromHostXML `xml:"msg_from_host"`
	XMLName       xml.Name         `xml:"scheduler_request"`
	Authenticator string           `xml:"authenticator"`
	// HostID is the id the project's server gave this computer (0 on the very
	// first contact) and RPCSeqno counts contacts. Both must be sent back:
	// without the host id every request registers a brand-new host on the
	// project's server.
	HostID   int `xml:"hostid"`
	RPCSeqno int `xml:"rpc_seqno"`
	// Platform is sent as platform_name — the tag a scheduler actually reads.
	// It used to be "platform", which no scheduler knows, so the server never
	// learned which platform to pick application versions for.
	Platform    string  `xml:"platform_name"`
	VersionNum  int     `xml:"version_num"`
	Timestamp   float64 `xml:"timestamp"`
	TeamID      int     `xml:"teamid"`
	TotalCredit float64 `xml:"total_credit"`
	Joined      int     `xml:"joined"`
	// This project's share of the total resource share, as the reference
	// client sends it. A bare top-level <resource_share> (what was sent
	// before) is not a scheduler_request tag, and newer scheduler versions
	// fail the entire request with "no end tag" because of it.
	ResourceShareFraction float64      `xml:"resource_share_fraction"`
	RRSFraction           float64      `xml:"rrs_fraction"`
	PRRSFraction          float64      `xml:"prrs_fraction"`
	HostInfo              *HostInfoXML `xml:"host_info"`
	Results               []ResultXML  `xml:"result"`
	CoreClientVer         string       `xml:"core_client_version"`
	// ClientBrand is shown by projects next to the client version (their host
	// list reads "8.0.2 (Iris)"); the numeric version stays BOINC-compatible
	// because schedulers refuse anything older than 5.8.
	ClientBrand string `xml:"client_brand"`
	// Coprocs lists the host's GPUs. The reference client writes it at the top
	// level of the request, next to host_info (not inside it).
	// AppVersions lists the person's own applications (app_info.xml) when the
	// platform is "anonymous".
	AppVersions *ClientAppVersionsXML `xml:"app_versions"`
	Coprocs     *CoprocsXML           `xml:"coprocs"`

	// A real BOINC scheduler reads the client's version from these three
	// fields (not from core_client_version) and rejects anything older than
	// 5.8.0 with "Need version 5.8.0 or higher ... You have 0.0.0" — which is
	// what every request answered before these were sent. SendRequest fills
	// them in from VersionNum.
	CoreClientMajor   int `xml:"core_client_major_version"`
	CoreClientMinor   int `xml:"core_client_minor_version"`
	CoreClientRelease int `xml:"core_client_release"`

	// Work-fetch: without these a scheduler has no idea Iris wants any work
	// at all and many will send none. WorkFetchRequest computes them from
	// how many CPU cores are free and how much is already queued for this
	// project.
	WorkReqSeconds  float64 `xml:"work_req_seconds"`
	CPUReqSecs      float64 `xml:"cpu_req_secs"`
	CPUReqInstances float64 `xml:"cpu_req_instances"`
}

type HostInfoXML struct {
	XMLName    xml.Name `xml:"host_info"`
	HostCPID   string   `xml:"host_cpid"`
	Timezone   int      `xml:"timezone"`
	DomainName string   `xml:"domain_name,omitempty"`
	// ProductName is the "Model" column of a project's host list.
	ProductName       string  `xml:"product_name,omitempty"`
	VirtualBoxVersion string  `xml:"virtualbox_version,omitempty"`
	DockerVersion     string  `xml:"docker_version,omitempty"`
	OsName            string  `xml:"os_name"`
	OsVersion         string  `xml:"os_version"`
	PVendor           string  `xml:"p_vendor"`
	PModel            string  `xml:"p_model"`
	PNcpus            int     `xml:"p_ncpus"`
	PFlops            float64 `xml:"p_fpops"`
	MNbytes           float64 `xml:"m_nbytes"`
	DFree             float64 `xml:"d_free"`
	DTotal            float64 `xml:"d_total"`
	ConnType          int     `xml:"conn_type"`
}

// ClientAppVersionsXML is the <app_versions> list an anonymous-platform client
// sends so the project only offers work for the applications it really has.
type ClientAppVersionsXML struct {
	Versions []ClientAppVersionXML `xml:"app_version"`
}

type ClientAppVersionXML struct {
	AppName    string           `xml:"app_name"`
	VersionNum int              `xml:"version_num"`
	Platform   string           `xml:"platform"`
	PlanClass  string           `xml:"plan_class"`
	AvgNCPUs   float64          `xml:"avg_ncpus"`
	Flops      float64          `xml:"flops"`
	Coproc     *ClientCoprocXML `xml:"coproc"`
}

type ClientCoprocXML struct {
	Type  string  `xml:"type"`
	Count float64 `xml:"count"`
}

// CoprocsXML is a host_info's GPU section, exactly as the reference client
// writes it (lib/coproc.cpp's COPROC_NVIDIA::write_xml /
// COPROC_ATI::write_xml): a nil pointer omits the whole <coprocs> element,
// matching a GPU-less host sending nothing rather than an empty tag.
type CoprocsXML struct {
	XMLName xml.Name        `xml:"coprocs"`
	CUDA    *CoprocCudaXML  `xml:"coproc_cuda"`
	ATI     *CoprocAtiXML   `xml:"coproc_ati"`
	Intel   *CoprocIntelXML `xml:"coproc_intel_gpu"`
	Apple   *CoprocAppleXML `xml:"coproc_apple_gpu"`
}

// CoprocCudaXML advertises the host's NVIDIA GPU(s) as one aggregate entry
// (BOINC assumes a homogeneous group per vendor). PeakFlops is deliberately
// left at 0 — Iris has no GPU compute benchmark, and a fabricated number
// would feed directly into a real project's work-fetch and deadline sizing,
// exactly the class of bug internal/scheduler's own CPU work-fetch fix this
// same session was about; projects fall back to their own default estimate
// for the plan class when it's absent.
type CoprocCudaXML struct {
	XMLName    xml.Name `xml:"coproc_cuda"`
	Count      int      `xml:"count"`
	Name       string   `xml:"name"`
	HaveCUDA   int      `xml:"have_cuda"`
	HaveOpenCL int      `xml:"have_opencl"`
	PeakFlops  float64  `xml:"peak_flops,omitempty"`
	// TotalGlobalMem is the video memory in bytes; without it a project lists
	// the card as having 0 MB.
	TotalGlobalMem float64 `xml:"totalGlobalMem,omitempty"`
	// Compute capability (e.g. 12/0), CUDA and driver versions: plan classes
	// refuse a device whose compute capability they see as 0.
	Major        int     `xml:"major,omitempty"`
	Minor        int     `xml:"minor,omitempty"`
	CudaVersion  int     `xml:"cudaVersion,omitempty"`
	DrvVersion   int     `xml:"drvVersion,omitempty"`
	ReqSecs      float64 `xml:"req_secs"`
	ReqInstances float64 `xml:"req_instances"`
	// What the server needs to size the card itself.
	MultiProcessorCount int              `xml:"multiProcessorCount,omitempty"`
	ClockRate           int              `xml:"clockRate,omitempty"`
	OpenCL              *CoprocOpenCLXML `xml:"coproc_opencl"`
}

// CoprocOpenCLXML is a GPU's OpenCL description, as the reference client writes it.
type CoprocOpenCLXML struct {
	Name              string `xml:"name"`
	Vendor            string `xml:"vendor"`
	VendorID          uint64 `xml:"vendor_id"`
	Available         int    `xml:"available"`
	HalfFP            uint64 `xml:"half_fp_config"`
	SingleFP          uint64 `xml:"single_fp_config"`
	DoubleFP          uint64 `xml:"double_fp_config"`
	EndianLittle      int    `xml:"endian_little"`
	ExecCaps          uint64 `xml:"execution_capabilities"`
	Extensions        string `xml:"extensions"`
	GlobalMem         uint64 `xml:"global_mem_size"`
	LocalMem          uint64 `xml:"local_mem_size"`
	MaxClock          uint64 `xml:"max_clock_frequency"`
	MaxCUs            uint64 `xml:"max_compute_units"`
	NvCCMajor         uint64 `xml:"nv_compute_capability_major"`
	NvCCMinor         uint64 `xml:"nv_compute_capability_minor"`
	AmdSimdPerCU      uint64 `xml:"amd_simd_per_compute_unit"`
	AmdSimdWidth      uint64 `xml:"amd_simd_width"`
	AmdSimdInstrWidth uint64 `xml:"amd_simd_instruction_width"`
	PlatformVersion   string `xml:"opencl_platform_version"`
	DeviceVersion     string `xml:"opencl_device_version"`
	DriverVersion     string `xml:"opencl_driver_version"`
}

// CoprocAtiXML is the AMD/ATI equivalent of CoprocCudaXML. HaveCAL is left
// at 0 (unknown) since Iris only detects the device, not the AMD compute
// driver stack; HaveOpenCL is still advertised since that's what most
// AMD-targeting BOINC plan classes actually check for.
type CoprocAtiXML struct {
	XMLName    xml.Name `xml:"coproc_ati"`
	Count      int      `xml:"count"`
	Name       string   `xml:"name"`
	HaveCAL    int      `xml:"have_cal"`
	HaveOpenCL int      `xml:"have_opencl"`
	PeakFlops  float64  `xml:"peak_flops,omitempty"`
	// LocalRAM is the video memory in megabytes (the reference client writes
	// this one in MB, unlike CUDA's bytes).
	LocalRAM     float64          `xml:"localRAM,omitempty"`
	ReqSecs      float64          `xml:"req_secs"`
	ReqInstances float64          `xml:"req_instances"`
	OpenCL       *CoprocOpenCLXML `xml:"coproc_opencl"`
}

// CoprocAppleXML is an Apple-silicon GPU (COPROC_APPLE::write_xml).
type CoprocAppleXML struct {
	Count        int              `xml:"count"`
	Model        string           `xml:"model"`
	AvailableRAM float64          `xml:"available_ram"`
	HaveMetal    int              `xml:"have_metal"`
	HaveOpenCL   int              `xml:"have_opencl"`
	NCores       int              `xml:"ncores"`
	MetalSupport int              `xml:"metal_support"`
	ReqSecs      float64          `xml:"req_secs"`
	ReqInstances float64          `xml:"req_instances"`
	OpenCL       *CoprocOpenCLXML `xml:"coproc_opencl"`
}

// CoprocIntelXML is an Intel integrated GPU (COPROC_INTEL::write_xml).
type CoprocIntelXML struct {
	Count        int              `xml:"count"`
	Name         string           `xml:"name"`
	AvailableRAM float64          `xml:"available_ram"`
	HaveOpenCL   int              `xml:"have_opencl"`
	ReqSecs      float64          `xml:"req_secs"`
	ReqInstances float64          `xml:"req_instances"`
	OpenCL       *CoprocOpenCLXML `xml:"coproc_opencl"`
}

// ResultXML is one finished task as the reference client reports it
// (RESULT::write with to_server=true in client/result.cpp): only completed
// results are ever sent, with their final times, exit status, the stderr text
// and a <file_info> for every output file that was uploaded first.
type ResultXML struct {
	XMLName          xml.Name        `xml:"result"`
	Name             string          `xml:"name"`
	FinalCPUTime     float64         `xml:"final_cpu_time"`
	FinalElapsedTime float64         `xml:"final_elapsed_time"`
	ExitStatus       int             `xml:"exit_status"`
	State            int             `xml:"state"`
	Platform         string          `xml:"platform"`
	VersionNum       int             `xml:"version_num"`
	PlanClass        string          `xml:"plan_class,omitempty"`
	AppVersionNum    int             `xml:"app_version_num,omitempty"`
	StderrOut        *RawXML         `xml:"stderr_out"`
	FileInfos        []ResultFileXML `xml:"file_info"`
}

// RawXML writes its text as-is (used for <stderr_out>, whose CDATA block must
// not be escaped).
type RawXML struct {
	Inner string `xml:",innerxml"`
}

// ResultFileXML is an uploaded output file, in FILE_INFO::write's to_server form.
type ResultFileXML struct {
	Name      string  `xml:"name"`
	NBytes    float64 `xml:"nbytes"`
	MaxNBytes float64 `xml:"max_nbytes"`
	MD5       string  `xml:"md5_cksum"`
}

// BOINC's result states as a scheduler expects them in a report.
const (
	ReportStateComputeError  = 3
	ReportStateFilesUploaded = 5
	maxStderrBytes           = 63 * 1024
)

// NewStderrOut builds the <stderr_out> block: the client version and the
// task's stderr text in a CDATA section, capped like the reference client.
func NewStderrOut(clientVersion, stderr string) *RawXML {
	if len(stderr) > maxStderrBytes {
		stderr = stderr[len(stderr)-maxStderrBytes:]
	}
	stderr = strings.ReplaceAll(strings.ToValidUTF8(stderr, "?"), "]]>", "]] >")
	var b strings.Builder
	b.WriteString("\n <core_client_version>" + clientVersion + "</core_client_version>\n")
	if strings.TrimSpace(stderr) != "" {
		b.WriteString("<![CDATA[\n" + stderr + "\n]]>\n")
	}
	return &RawXML{Inner: b.String()}
}

type Reply struct {
	XMLName      xml.Name `xml:"scheduler_reply"`
	Error        string   `xml:"error"`
	TotalCredit  float64  `xml:"total_credit"`
	ExpAvgCredit float64  `xml:"expavg_credit"`

	UserTotalCredit  float64 `xml:"user_total_credit"`
	UserExpavgCredit float64 `xml:"user_expavg_credit"`
	HostTotalCredit  float64 `xml:"host_total_credit"`
	HostExpavgCredit float64 `xml:"host_expavg_credit"`

	ResourceShare float64 `xml:"resource_share"`
	// Messages are the server's own explanations ("no work available", "invalid
	// account key", ...). A reply can carry several.
	Messages   []ReplyMessage `xml:"message"`
	ServerTime float64        `xml:"server_time"`
	// RequestDelay is how many seconds the server wants us to wait before the
	// next request (BOINC's <request_delay>). It used to be read from a tag
	// named "delay", which no scheduler sends, so the server's back-off was
	// never honored.
	RequestDelay float64 `xml:"request_delay"`
	// HostID is the id the server assigned this computer; it has to be stored
	// and sent back with every later request.
	HostID int `xml:"hostid"`
	// ProjectName is the project's own name, which is how a project attached
	// without one (or with only its URL) gets a real name.
	ProjectName   string          `xml:"project_name"`
	FileInfos     []FileInfoXML   `xml:"file_info"`
	FileTransfers []ReplyFileXfer `xml:"file_transfer"`
	Results       []ReplyResult   `xml:"result"`
	// Workunits carry each task's input files and command line; a <result>
	// only names its workunit (wu_name) and lists its OUTPUT files.
	Workunits []WorkunitXML `xml:"workunit"`
	// ResultAcks confirm results we reported; only those may be forgotten.
	ResultAcks []ResultAckXML `xml:"result_ack"`
	// ResultAborts name tasks the server no longer wants computed.
	ResultAborts []ResultAckXML `xml:"result_abort"`
	// TrickleDowns are messages for running tasks; MessageAck confirms the
	// trickle-ups of this request were received.
	TrickleDowns []TrickleDownXML `xml:"trickle_down"`
	MessageAck   *struct{}        `xml:"message_ack"`
	// AppVersions describes the real, project-specific executables the
	// scheduler is offering for the platforms/plan classes this host asked
	// about. A ReplyResult only carries enough (app_version_num, plan_class)
	// to look one of these up — see engine.go's matchAppVersion.
	AppVersions []AppVersionXML `xml:"app_version"`
}

// AppVersionXML describes one real application build a project ships,
// exactly as BOINC's own scheduler reply does:
// https://github.com/BOINC/boinc/wiki/XmlFormat
type AppVersionXML struct {
	XMLName    xml.Name        `xml:"app_version"`
	AppName    string          `xml:"app_name"`
	VersionNum int             `xml:"version_num"`
	Platform   string          `xml:"platform"`
	PlanClass  string          `xml:"plan_class"`
	AvgNCPUs   float64         `xml:"avg_ncpus"`
	FileRef    []AppFileRefXML `xml:"file_ref"`
}

// AppFileRefXML is a file_ref as it appears inside an app_version: it names
// one of the files the app version needs (looked up in Reply.FileInfos for
// its download URL, same as a result's own file_ref) and, for the one file
// that is the actual executable, carries an empty <main_program/> tag.
type AppFileRefXML struct {
	XMLName     xml.Name  `xml:"file_ref"`
	FileName    string    `xml:"file_name"`
	OpenName    string    `xml:"open_name"`
	MainProgram *struct{} `xml:"main_program"`
}

// ReplyMessage is one <message priority="...">text</message> from a scheduler.
type ReplyMessage struct {
	Priority string `xml:"priority,attr"`
	Text     string `xml:",chardata"`
}

type ReplyFileXfer struct {
	XMLName xml.Name `xml:"file_transfer"`
	Name    string   `xml:"name"`
	URL     string   `xml:"url"`
	NBytes  float64  `xml:"nbytes"`
}

// WorkunitXML is a <workunit> of a scheduler reply (WORKUNIT::parse).
type WorkunitXML struct {
	XMLName    xml.Name     `xml:"workunit"`
	Name       string       `xml:"name"`
	AppName    string       `xml:"app_name"`
	VersionNum int          `xml:"version_num"`
	CmdLine    string       `xml:"command_line"`
	FileRef    []FileRefXML `xml:"file_ref"`
	// RscFpopsEst is the project's estimate of the work in floating point
	// operations, which is how a task with no progress reports gets a
	// realistic progress bar.
	RscFpopsEst float64 `xml:"rsc_fpops_est"`
}

// ResultAckXML is a <result_ack> / <result_abort> entry.
type ResultAckXML struct {
	Name string `xml:"name"`
}

// FileInfoXML is a <file_info> of a scheduler reply (FILE_INFO::parse): an
// input or application file to download, or — marked generated_locally — an
// output file the task must produce and upload, with the signed upload
// certificate the file upload handler checks.
type FileInfoXML struct {
	XMLName       xml.Name  `xml:"file_info"`
	Name          string    `xml:"name"`
	URLs          []string  `xml:"url"`
	DownloadURLs  []string  `xml:"download_url"`
	UploadURLs    []string  `xml:"upload_url"`
	NBytes        float64   `xml:"nbytes"`
	MaxNBytes     float64   `xml:"max_nbytes"`
	MD5           string    `xml:"md5"`
	MD5Cksum      string    `xml:"md5_cksum"`
	XMLSignature  string    `xml:"xml_signature"`
	Generated     *struct{} `xml:"generated_locally"`
	UploadPresent *struct{} `xml:"upload_when_present"`
}

// Checksum is the file's MD5 (real servers write <md5_cksum>).
func (f FileInfoXML) Checksum() string {
	if f.MD5Cksum != "" {
		return f.MD5Cksum
	}
	return f.MD5
}

// DownloadURL is the first address to fetch the file from.
func (f FileInfoXML) DownloadURL() string {
	if len(f.DownloadURLs) > 0 {
		return f.DownloadURLs[0]
	}
	if len(f.URLs) > 0 {
		return f.URLs[0]
	}
	return ""
}

// UploadTargets lists every address the file may be uploaded to.
func (f FileInfoXML) UploadTargets() []string {
	if len(f.UploadURLs) > 0 {
		return f.UploadURLs
	}
	return f.URLs
}

// IsOutput reports whether the file is one the task produces.
func (f FileInfoXML) IsOutput() bool {
	return f.Generated != nil || f.UploadPresent != nil
}

type ReplyResult struct {
	XMLName          xml.Name     `xml:"result"`
	Name             string       `xml:"name"`
	WuName           string       `xml:"wu_name"`
	FractionDone     float64      `xml:"fraction_done"`
	Priority         float64      `xml:"priority"`
	ReportDeadline   float64      `xml:"report_deadline"`
	EstimatedFlops   float64      `xml:"estimated_fpops"`
	MaxElapSec       float64      `xml:"max_elap_sec"`
	AppVersionNum    int          `xml:"app_version_num"`
	EarliestDeadline float64      `xml:"earliest_deadline"`
	StdOut           string       `xml:"stdout_out"`
	CmdLine          string       `xml:"cmd_line"`
	RsEnd            string       `xml:"rs_end"`
	PlanClass        string       `xml:"plan_class"`
	VersionNum       int          `xml:"version_num"`
	Platform         string       `xml:"platform"`
	FileRef          []FileRefXML `xml:"file_ref"`
}

// FileRefXML is a <file_ref>: which file (by physical name), under which
// logical name the application opens it (open_name), and for an output whether
// the client copies rather than moves it.
type FileRefXML struct {
	XMLName  xml.Name  `xml:"file_ref"`
	Name     string    `xml:"file_name"`
	OpenName string    `xml:"open_name"`
	MD5      string    `xml:"md5"`
	Optional *struct{} `xml:"optional"`
}

func NewClient(projectURL string) *Client {
	return &Client{
		projectURL: strings.TrimRight(projectURL, "/"),
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: false},
			},
		},
	}
}

func (c *Client) SetAuth(authToken string) {
	c.authToken = authToken
}

func (c *Client) baseURL() string {
	s := strings.TrimRight(c.projectURL, "/")
	scheme := "https://"
	if strings.HasPrefix(s, "http://") {
		scheme = "http://"
		s = strings.TrimPrefix(s, "http://")
	} else {
		s = strings.TrimPrefix(s, "https://")
	}
	parts := strings.SplitN(s, "/", 2)
	host := parts[0]
	path := ""
	if len(parts) > 1 {
		path = "/" + parts[1]
	}
	return scheme + host + path
}

// GetSchedulerURL returns the URL a scheduler request is sent to: whatever
// SetSchedulerURL last set (normally the result of DiscoverSchedulerURL), or
// else the old "cgi-bin/scheduler" convention as a last-resort guess for
// projects whose master page publishes nothing.
func (c *Client) GetSchedulerURL() string {
	if c.schedulerURL != "" {
		return c.schedulerURL
	}
	return c.baseURL() + "/cgi-bin/scheduler"
}

// SetSchedulerURL overrides the address a scheduler request is sent to.
func (c *Client) SetSchedulerURL(url string) {
	c.schedulerURL = url
}

var (
	linkSchedulerRe = regexp.MustCompile(`(?is)<link[^>]+rel=["']boinc_scheduler["'][^>]*href=["']([^"']+)["']`)
	tagSchedulerRe  = regexp.MustCompile(`(?is)<scheduler>\s*([^<\s]+)\s*</scheduler>`)
)

// DiscoverSchedulerURL fetches the project's master page and scrapes the
// scheduler address(es) it publishes there, since a real, actively-run
// project's scheduler is almost never simply "<master_url>/cgi-bin/scheduler"
// — Einstein@Home's, for example, is on an entirely different subdomain and
// path. BOINC projects publish it either as a
// <link rel="boinc_scheduler" href="..."> tag, or the older
// <scheduler>...</scheduler> tag (often inside an HTML comment — a plain
// substring search finds it either way). A reply's own <redirect> elements
// (not implemented here) are meant to keep this list current after the
// first successful contact; this covers the common case of the very first
// one.
func (c *Client) DiscoverSchedulerURL(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL()+"/", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", product.UserAgent())
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}

	var urls []string
	seen := map[string]bool{}
	add := func(u string) {
		u = strings.TrimSpace(u)
		if u != "" && !seen[u] {
			urls = append(urls, u)
			seen[u] = true
		}
	}
	for _, m := range linkSchedulerRe.FindAllSubmatch(body, -1) {
		add(string(m[1]))
	}
	for _, m := range tagSchedulerRe.FindAllSubmatch(body, -1) {
		add(string(m[1]))
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("no scheduler address published on the project's master page")
	}
	return urls, nil
}

func (c *Client) GetFileURL(filename string) string {
	return c.baseURL() + "/file.php?name=" + filename
}

// marshalRequest renders a scheduler request the way a real BOINC scheduler
// can parse it. Those servers read the request line by line, so a normal
// compact encoding/xml document (everything on one line, no final newline)
// is answered with "Error in request message: no end tag" and no work — which
// is exactly what happened to every request until this was found by sending
// the same request to a live project in several layouts: one tag per line
// plus a newline after </scheduler_request> is what gets it accepted.
func marshalRequest(req *Request) ([]byte, error) {
	data, err := xml.MarshalIndent(req, "", " ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func (c *Client) SendRequest(req *Request) (*Reply, error) {
	if c.authToken != "" {
		req.Authenticator = c.authToken
	}
	if req.ClientBrand == "" {
		req.ClientBrand = product.Name + " " + product.Version
	}
	if req.CoreClientVer == "" {
		req.CoreClientVer = product.UserAgent()
	}
	if req.Timestamp == 0 {
		req.Timestamp = float64(time.Now().Unix())
	}
	if req.CoreClientMajor == 0 && req.VersionNum > 0 {
		req.CoreClientMajor = req.VersionNum / 100
		req.CoreClientMinor = req.VersionNum / 10 % 10
		req.CoreClientRelease = req.VersionNum % 10
	}

	data, err := marshalRequest(req)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	resp, err := postFollow(context.Background(), c.httpClient, c.GetSchedulerURL(), "text/xml", func() (io.Reader, int64, error) {
		return bytes.NewReader(data), int64(len(data)), nil
	})
	if err != nil {
		return nil, fmt.Errorf("POST: %w", err)
	}
	defer resp.Body.Close()

	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("gzip: %w", err)
		}
		defer gz.Close()
		reader = gz
	}

	var reply Reply
	r := bufio.NewReader(reader)
	if err := xml.NewDecoder(r).Decode(&reply); err != nil {
		raw, _ := io.ReadAll(reader)
		return nil, fmt.Errorf("decode: %w (raw: %.200s)", err, string(raw))
	}

	return &reply, nil
}

// ProgressFunc reports transfer progress; total is 0 when the size is unknown.
type ProgressFunc func(done, total int64)

// stallTimeout is how long a transfer may go without moving a byte.
const stallTimeout = 60 * time.Second

func (c *Client) DownloadFile(filename, destPath string) error {
	return c.DownloadFileCtx(context.Background(), filename, destPath, nil)
}

func (c *Client) DownloadFileCtx(ctx context.Context, filename, destPath string, progress ProgressFunc) error {
	return downloadURL(ctx, c.GetFileURL(filename), destPath, progress)
}

func DownloadFileByURL(rawURL, destPath string) error {
	return downloadURL(context.Background(), rawURL, destPath, nil)
}

func DownloadFileByURLCtx(ctx context.Context, rawURL, destPath string, progress ProgressFunc) error {
	return downloadURL(ctx, rawURL, destPath, progress)
}

// transferClient has no overall timeout: large work files legitimately take
// minutes. Stalls are caught by the idle watchdog instead.
var transferClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSHandshakeTimeout:   20 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		IdleConnTimeout:       60 * time.Second,
	},
}

// idleReader feeds progress and re-arms the stall watchdog on every read.
type idleReader struct {
	r        io.Reader
	done     int64
	total    int64
	progress ProgressFunc
	timer    *time.Timer
}

func (ir *idleReader) Read(p []byte) (int, error) {
	n, err := ir.r.Read(p)
	if n > 0 {
		ir.done += int64(n)
		ir.timer.Reset(stallTimeout)
		if ir.progress != nil {
			ir.progress(ir.done, ir.total)
		}
	}
	return n, err
}

func downloadURL(ctx context.Context, url, destPath string, progress ProgressFunc) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stall := time.AfterFunc(stallTimeout, cancel)
	defer stall.Stop()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	resp, err := transferClient.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}

	var body io.Reader = resp.Body
	total := resp.ContentLength
	if total < 0 {
		total = 0
	}
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return fmt.Errorf("gzip %s: %w", url, err)
		}
		defer gz.Close()
		body = gz
		total = 0
	}
	ir := &idleReader{r: body, total: total, progress: progress, timer: stall}

	// Write to a temporary name so a half-finished file is never mistaken for
	// a complete one.
	tmp := destPath + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create %s: %w", tmp, err)
	}
	_, copyErr := io.Copy(f, ir)
	closeErr := f.Close()
	if copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		os.Remove(tmp)
		if ctx.Err() != nil && errors.Is(copyErr, context.Canceled) {
			return fmt.Errorf("download %s: cancelled or stalled", url)
		}
		return fmt.Errorf("download %s: %w", url, copyErr)
	}
	if err := os.Rename(tmp, destPath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("finalize %s: %w", destPath, err)
	}
	return nil
}

func (c *Client) UploadFile(filePath, projectName string) error {
	return c.UploadFileCtx(context.Background(), filePath, projectName, nil)
}

func (c *Client) UploadFileCtx(ctx context.Context, filePath, projectName string, progress ProgressFunc) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open %s: %w", filePath, err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", filePath, err)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stall := time.AfterFunc(stallTimeout, cancel)
	defer stall.Stop()

	const boundary = "irisboundary7d8f1c"
	fname := strings.NewReplacer(`\`, `\`, `"`, `\"`, "\r", "", "\n", "").Replace(filepath.Base(filePath))
	head := "--" + boundary + "\r\n" +
		`Content-Disposition: form-data; name="file"; filename="` + fname + `"` + "\r\n" +
		"Content-Type: application/octet-stream\r\n\r\n"
	tail := "\r\n--" + boundary + "--\r\n"
	ir := &idleReader{r: f, total: st.Size(), progress: progress, timer: stall}
	body := io.MultiReader(strings.NewReader(head), ir, strings.NewReader(tail))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.GetUploadURL(), body)
	if err != nil {
		return fmt.Errorf("upload %s: %w", filePath, err)
	}
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.ContentLength = int64(len(head)) + st.Size() + int64(len(tail))
	resp, err := transferClient.Do(req)
	if err != nil {
		return fmt.Errorf("upload %s: %w", filePath, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("upload %s: HTTP %d (%s)", filePath, resp.StatusCode, string(raw))
	}
	return nil
}

func (c *Client) GetUploadURL() string {
	return c.baseURL() + "/cgi-bin/file_upload_handler"
}
