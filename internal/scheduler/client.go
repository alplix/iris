package scheduler

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/tls"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alplix/iris/internal/product"
)

type Client struct {
	projectURL string
	authToken  string
	httpClient *http.Client
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
	XMLName       xml.Name        `xml:"scheduler_reply"`
	Error         string          `xml:"error"`
	TotalCredit   float64         `xml:"total_credit"`
	ExpAvgCredit  float64         `xml:"expavg_credit"`
	ResourceShare float64         `xml:"resource_share"`
	Message       string          `xml:"message"`
	ServerTime    float64         `xml:"server_time"`
	Delay         int             `xml:"delay"`
	FileInfos     []FileInfoXML   `xml:"file_info"`
	FileTransfers []ReplyFileXfer `xml:"file_transfer"`
	Results       []ReplyResult   `xml:"result"`
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

func (c *Client) GetSchedulerURL() string {
	return c.baseURL() + "/cgi-bin/scheduler"
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

func (c *Client) DownloadFile(filename, destPath string) error {
	url := c.GetFileURL(filename)
	return downloadURL(url, destPath)
}

func DownloadFileByURL(rawURL, destPath string) error {
	return downloadURL(rawURL, destPath)
}

func downloadURL(url, destPath string) error {
	httpClient := &http.Client{Timeout: 60 * time.Second}
	resp, err := httpClient.Get(url)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}

	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return fmt.Errorf("gzip %s: %w", url, err)
		}
		defer gz.Close()
		reader = gz
	}

	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", destPath, err)
	}
	defer f.Close()

	_, err = io.Copy(f, reader)
	return err
}

func (c *Client) UploadFile(filePath, projectName string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open %s: %w", filePath, err)
	}
	defer f.Close()

	uploadURL := c.GetUploadURL()
	var buf bytes.Buffer
	buf.WriteString("--boundary\r\n")
	buf.WriteString(fmt.Sprintf("Content-Disposition: form-data; name=\"file\"; filename=\"%s\"\r\n", filepath.Base(filePath)))
	buf.WriteString("Content-Type: application/octet-stream\r\n\r\n")
	io.Copy(&buf, f)
	buf.WriteString("\r\n--boundary--\r\n")

	resp, err := c.httpClient.Post(uploadURL, "multipart/form-data; boundary=boundary", &buf)
	if err != nil {
		return fmt.Errorf("upload %s: %w", filePath, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload %s: HTTP %d (%s)", filePath, resp.StatusCode, string(raw))
	}
	return nil
}

func (c *Client) GetUploadURL() string {
	return c.baseURL() + "/cgi-bin/file_upload_handler"
}
