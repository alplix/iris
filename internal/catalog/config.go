package catalog

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Config is what a project server says about itself on get_project_config.php,
// the call the BOINC manager makes when a project is chosen.
type Config struct {
	Name      string `json:"name"`
	MasterURL string `json:"masterUrl"`
	// AccountCreationDisabled is set when new accounts cannot be created from
	// the client (they can still be made on the project's website).
	AccountCreationDisabled bool     `json:"accountCreationDisabled"`
	MinPasswordLength       int      `json:"minPasswordLength"`
	TermsOfUse              string   `json:"termsOfUse,omitempty"`
	Platforms               []string `json:"platforms"`
	WebRPCURLBase           string   `json:"webRpcUrlBase,omitempty"`
}

type xmlConfig struct {
	Name           string    `xml:"name"`
	MasterURL      string    `xml:"master_url"`
	WebRPCURLBase  string    `xml:"web_rpc_url_base"`
	MinPasswd      int       `xml:"min_passwd_length"`
	Disabled       *struct{} `xml:"account_creation_disabled"`
	ClientDisabled *struct{} `xml:"client_account_creation_disabled"`
	Terms          string    `xml:"terms_of_use"`
	Platforms      []string  `xml:"platforms>platform>platform_name"`
	ErrorNum       int       `xml:"error_num"`
	ErrorMsg       string    `xml:"error_msg"`
}

// ParseConfig reads the XML of get_project_config.php.
func ParseConfig(r io.Reader) (*Config, error) {
	dec := xml.NewDecoder(r)
	dec.CharsetReader = charsetReader
	var x xmlConfig
	if err := dec.Decode(&x); err != nil {
		return nil, fmt.Errorf("not a BOINC project: %w", err)
	}
	if x.ErrorNum != 0 {
		return nil, fmt.Errorf("project error %d: %s", x.ErrorNum, clean(x.ErrorMsg))
	}
	if x.Name == "" && x.MasterURL == "" {
		return nil, fmt.Errorf("not a BOINC project")
	}
	terms := clean(x.Terms)
	if len(terms) > 1500 {
		terms = terms[:1500] + "…"
	}
	return &Config{
		Name: clean(x.Name), MasterURL: strings.TrimSpace(x.MasterURL),
		AccountCreationDisabled: x.Disabled != nil || x.ClientDisabled != nil,
		MinPasswordLength:       x.MinPasswd, TermsOfUse: terms,
		Platforms: BasePlatforms(x.Platforms), WebRPCURLBase: strings.TrimSpace(x.WebRPCURLBase),
	}, nil
}

// FetchConfig asks a project server for its configuration. It doubles as a
// liveness check: a project that does not answer cannot be attached.
func FetchConfig(ctx context.Context, projectURL string) (*Config, error) {
	projectURL = strings.TrimSpace(projectURL)
	if !validURL(projectURL) {
		return nil, fmt.Errorf("the project address must start with http:// or https://")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(projectURL, "/")+"/get_project_config.php", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return ParseConfig(io.LimitReader(resp.Body, 1<<20))
}
