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
	XMLName       xml.Name     `xml:"scheduler_request"`
	Authenticator string       `xml:"authenticator"`
	HostCPID      string       `xml:"host_cpid"`
	Platform      string       `xml:"platform"`
	VersionNum    int          `xml:"version_num"`
	Timestamp     float64      `xml:"timestamp"`
	TeamID        int          `xml:"teamid"`
	TotalCredit   float64      `xml:"total_credit"`
	Joined        int          `xml:"joined"`
	ResourceShare float64      `xml:"resource_share"`
	HostInfo      *HostInfoXML `xml:"host_info"`
	Results       []ResultXML  `xml:"result"`
	CoreClientVer string       `xml:"core_client_version"`

	// Work-fetch: without these a scheduler has no idea Iris wants any work
	// at all and many will send none. WorkFetchRequest computes them from
	// how many CPU cores are free and how much is already queued for this
	// project.
	WorkReqSeconds  float64 `xml:"work_req_seconds"`
	CPUReqSecs      float64 `xml:"cpu_req_secs"`
	CPUReqInstances float64 `xml:"cpu_req_instances"`
}

type HostInfoXML struct {
	XMLName   xml.Name `xml:"host_info"`
	OsName    string   `xml:"os_name"`
	OsVersion string   `xml:"os_version"`
	PVendor   string   `xml:"p_vendor"`
	PModel    string   `xml:"p_model"`
	PNcpus    int      `xml:"p_ncpus"`
	PFlops    float64  `xml:"p_fpops"`
	MNbytes   float64  `xml:"m_nbytes"`
	DFree     float64  `xml:"d_free"`
	DTotal    float64  `xml:"d_total"`
	ConnType  int      `xml:"conn_type"`
}

type ResultXML struct {
	XMLName        xml.Name `xml:"result"`
	Name           string   `xml:"name"`
	WuName         string   `xml:"wu_name"`
	ProjectURL     string   `xml:"project_url"`
	FractionDone   float64  `xml:"fraction_done"`
	CPUTime        float64  `xml:"cpu_time"`
	ExitStatus     int      `xml:"exit_status"`
	State          int      `xml:"state"`
	Platform       string   `xml:"platform"`
	VersionNum     int      `xml:"version_num"`
	ReportDeadline float64  `xml:"report_deadline"`
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

	ResourceShare float64         `xml:"resource_share"`
	Message       string          `xml:"message"`
	ServerTime    float64         `xml:"server_time"`
	Delay         int             `xml:"delay"`
	FileInfos     []FileInfoXML   `xml:"file_info"`
	FileTransfers []ReplyFileXfer `xml:"file_transfer"`
	Results       []ReplyResult   `xml:"result"`
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
	MainProgram *struct{} `xml:"main_program"`
}

type ReplyFileXfer struct {
	XMLName xml.Name `xml:"file_transfer"`
	Name    string   `xml:"name"`
	URL     string   `xml:"url"`
	NBytes  float64  `xml:"nbytes"`
}

type FileInfoXML struct {
	XMLName xml.Name `xml:"file_info"`
	Name    string   `xml:"name"`
	URL     string   `xml:"url"`
	NBytes  float64  `xml:"nbytes"`
	MD5     string   `xml:"md5"`
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
	FileRef          []FileRefXML `xml:"file_ref"`
}

type FileRefXML struct {
	XMLName xml.Name `xml:"file_ref"`
	Name    string   `xml:"file_name"`
	MD5     string   `xml:"md5"`
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

func (c *Client) SendRequest(req *Request) (*Reply, error) {
	if c.authToken != "" {
		req.Authenticator = c.authToken
	}
	if req.CoreClientVer == "" {
		req.CoreClientVer = product.UserAgent()
	}
	if req.Timestamp == 0 {
		req.Timestamp = float64(time.Now().Unix())
	}

	data, err := xml.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	resp, err := c.httpClient.Post(c.GetSchedulerURL(), "text/xml", bytes.NewReader(data))
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
