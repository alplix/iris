package boinc

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

func (c *Client) GetState() (*ClientState, error) {
	frame, err := c.Call("<get_state/>")
	if err != nil {
		return nil, err
	}
	var st ClientState
	if err := ParseInto(frame, &st); err != nil {
		return nil, fmt.Errorf("bad state reply: %w", err)
	}
	return &st, nil
}

func (c *Client) GetTransfers() ([]FileTransfer, error) {
	frame, err := c.Call("<get_file_transfers/>")
	if err != nil {
		return nil, err
	}
	var d struct {
		T []FileTransfer `xml:"file_transfers>file_transfer"`
	}
	err = ParseWrapped(frame, &d)
	return d.T, err
}

func (c *Client) GetCcStatus() (*CcStatus, error) {
	frame, err := c.Call("<get_cc_status/>")
	if err != nil {
		return nil, err
	}
	var d struct {
		S CcStatus `xml:"cc_status"`
	}
	err = ParseWrapped(frame, &d)
	return &d.S, err
}

func (c *Client) GetMessages(after int) ([]Msg, error) {
	xmlReq := "<get_messages/>"
	if after >= 0 {
		xmlReq = fmt.Sprintf("<get_messages>\n <seqno>%d</seqno>\n</get_messages>", after)
	}
	frame, err := c.Call(xmlReq)
	if err != nil {
		return nil, err
	}
	var d struct {
		M []Msg `xml:"msgs>msg"`
	}
	err = ParseWrapped(frame, &d)
	return d.M, err
}

func (c *Client) GetStats() ([]ProjectStats, error) {
	frame, err := c.Call("<get_statistics/>")
	if err != nil {
		return nil, err
	}
	var d struct {
		S []ProjectStats `xml:"statistics>project_statistics"`
	}
	err = ParseWrapped(frame, &d)
	return d.S, err
}

func (c *Client) GetDailyXferHistory() ([]DailyXfer, error) {
	frame, err := c.Call("<get_daily_xfer_history/>")
	if err != nil {
		return nil, err
	}
	var d struct {
		X []DailyXfer `xml:"daily_xfers>dx"`
	}
	err = ParseWrapped(frame, &d)
	return d.X, err
}

func (c *Client) GetDiskUsage() (*DiskUsage, error) {
	frame, err := c.Call("<get_disk_usage/>")
	if err != nil {
		return nil, err
	}
	var d DiskUsage
	if err := ParseInto(frame, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func (c *Client) GetPrefsOverride() (map[string]string, error) {
	frame, err := c.Call("<get_global_prefs_override/>")
	if err != nil {
		return nil, err
	}
	return ParseOverride(frame), nil
}

var prefKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

func (c *Client) SetPrefsOverride(pairs [][2]string) error {
	// Keys become XML element names, so anything else could inject markup.
	for _, p := range pairs {
		if !prefKeyRe.MatchString(p[0]) {
			return fmt.Errorf("invalid preference name %q", p[0])
		}
	}
	var b strings.Builder
	b.WriteString("<set_global_prefs_override>\n<global_prefs_override>")
	for _, p := range pairs {
		fmt.Fprintf(&b, "\n <%s>%s</%s>", p[0], EscapeXML(p[1]), p[0])
	}
	b.WriteString("\n</global_prefs_override>\n</set_global_prefs_override>")
	frame, err := c.Call(b.String())
	if err != nil {
		return err
	}
	if !regexp.MustCompile(`(?s)<success\s*/>`).Match(frame) {
		return fmt.Errorf("failed to save preferences")
	}
	return nil
}

func (c *Client) SetRunMode(mode string) error {
	_, err := c.Call(fmt.Sprintf("<set_run_mode>\n <mode>%s</mode>\n <duration>0</duration>\n</set_run_mode>", mode))
	return err
}

func (c *Client) SetNetworkMode(mode string) error {
	_, err := c.Call(fmt.Sprintf("<set_network_mode>\n <mode>%s</mode>\n <duration>0</duration>\n</set_network_mode>", mode))
	return err
}

func (c *Client) RunBenchmarks() error {
	_, err := c.Call("<run_benchmarks/>")
	return err
}

func (c *Client) ResultOp(name, op string) error {
	_, err := c.Call(fmt.Sprintf("<result_op>\n <name>%s</name>\n <operation>%s</operation>\n</result_op>", EscapeXML(name), op))
	return err
}

func (c *Client) ProjectOp(url, op string) error {
	_, err := c.Call(fmt.Sprintf("<project_op>\n <project_url>%s</project_url>\n <operation>%s</operation>\n</project_op>", EscapeXML(url), op))
	return err
}

func (c *Client) FileTransferOp(name, op string) error {
	_, err := c.Call(fmt.Sprintf("<file_transfer_op>\n <ft_name>%s</ft_name>\n <operation>%s</operation>\n</file_transfer_op>", EscapeXML(name), op))
	return err
}

func (c *Client) ProjectAttach(url, authenticator, name string) error {
	extra := ""
	if name != "" {
		extra = fmt.Sprintf(" <project_name>%s</project_name>\n", EscapeXML(name))
	}
	_, err := c.Call(fmt.Sprintf("<project_attach>\n <project_url>%s</project_url>\n <authenticator>%s</authenticator>\n%s</project_attach>",
		EscapeXML(url), EscapeXML(authenticator), extra))
	return err
}

var (
	authRe   = regexp.MustCompile(`(?s)<authenticator>\s*(.*?)\s*</authenticator>`)
	errNumRe = regexp.MustCompile(`(?s)<error_num>\s*(-?\d+)\s*</error_num>`)
	errMsgRe = regexp.MustCompile(`(?s)<error_msg>\s*(.*?)\s*</error_msg>`)
)

// boincPasswdHash is the password hash BOINC servers expect everywhere a
// client authenticates with an email + password instead of an account key:
// md5(password + lowercase(email)). The client never sends the plaintext
// password over the network.
func boincPasswdHash(password, email string) string {
	sum := md5.Sum([]byte(password + strings.ToLower(email)))
	return hex.EncodeToString(sum[:])
}

func LookupAccount(baseURL, email, password string) (string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	url := strings.TrimSuffix(baseURL, "/") + "/lookup_account.php?email_addr=" +
		escapeQuery(email) + "&passwd_hash=" + boincPasswdHash(password, email)
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("account lookup failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if m := authRe.FindStringSubmatch(string(body)); m != nil && m[1] != "" {
		return m[1], nil
	}
	if m := errMsgRe.FindStringSubmatch(string(body)); m != nil && m[1] != "" {
		return "", fmt.Errorf("%s (%s)", m[1], baseURL)
	}
	if m := errNumRe.FindStringSubmatch(string(body)); m != nil {
		return "", fmt.Errorf("account not found or wrong password on %s (error %s)", baseURL, m[1])
	}
	return "", fmt.Errorf("account not found or wrong password on %s", baseURL)
}

func escapeQuery(s string) string {
	r := strings.NewReplacer("&", "%26", "+", "%2B", " ", "%20", "#", "%23", "?", "%3F", "=", "%3D")
	return r.Replace(s)
}
