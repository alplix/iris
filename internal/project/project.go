package project

import (
	"crypto/md5"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Account struct {
	XMLName       xml.Name `xml:"account"`
	ProjectURL    string   `xml:"project_url"`
	UserName      string   `xml:"user_name"`
	Authenticator string   `xml:"authenticator"`
	TeamID        int      `xml:"team_id"`
	UserID        int      `xml:"user_id"`
	HostID        int      `xml:"host_id"`
	TotalCredit   float64  `xml:"total_credit"`
	ExpAvgCredit  float64  `xml:"expavg_credit"`
	ResourceShare float64  `xml:"resource_share"`
	Joined        int      `xml:"joined"`
	Venue         string   `xml:"venue"`
}

type ProjectDir struct {
	base string
}

func NewDir(dataDir, projectURL string) *ProjectDir {
	return &ProjectDir{base: filepath.Join(dataDir, "projects", DirName(projectURL))}
}

// Init creates the project's folder. It stays otherwise empty: like the
// reference client's, it holds account.xml, the files the project sends, and
// whatever the person adds (app_info.xml and the application files it names).
func (pd *ProjectDir) Init() error {
	return os.MkdirAll(pd.base, 0o755)
}

func (pd *ProjectDir) SaveAccount(acct *Account) error {
	data, err := xml.MarshalIndent(acct, "", "  ")
	if err != nil {
		return err
	}
	var buf strings.Builder
	buf.WriteString(xml.Header)
	buf.Write(data)
	buf.WriteString("\n")
	return os.WriteFile(filepath.Join(pd.base, "account.xml"), []byte(buf.String()), 0o644)
}

func (pd *ProjectDir) WriteConfig(content string) error {
	return os.WriteFile(filepath.Join(pd.base, "cc_config.xml"), []byte(content), 0o644)
}

func (pd *ProjectDir) Path() string {
	return pd.base
}

func urlHash(url string) string {
	h := md5.Sum([]byte(url))
	return fmt.Sprintf("%x", h)
}

func GenCCConfig(opts CCOptions) string {
	var buf strings.Builder
	buf.WriteString(xml.Header)
	buf.WriteString("<cc_config>\n<options>\n")
	buf.WriteString(fmt.Sprintf("  <user_agent>%s</user_agent>\n", opts.UserAgent))
	if opts.AllowRemoteGuiRPC {
		buf.WriteString("  <allow_remote_gui_rpc/>\n")
	}
	if opts.ReportResultsEarly {
		buf.WriteString("  <report_results_early/>\n")
	}
	if opts.UseAllGPUs {
		buf.WriteString("  <use_all_gpus/>\n")
	}
	buf.WriteString(fmt.Sprintf("  <max_app_clients>%d</max_app_clients>\n", opts.MaxAppClients))
	buf.WriteString("</options>\n<log_flags/>\n</cc_config>\n")
	return buf.String()
}

type CCOptions struct {
	UserAgent          string
	AllowRemoteGuiRPC  bool
	MaxAppClients      int
	ReportResultsEarly bool
	UseAllGPUs         bool
}
