package catalog

import (
	"context"
	"strings"
	"testing"
)

const configXML = `<?xml version="1.0" ?>
<project_config>
  <name>Einstein@Home</name>
  <master_url>https://einsteinathome.org/</master_url>
  <web_rpc_url_base>https://einsteinathome.org/</web_rpc_url_base>
  <min_passwd_length>6</min_passwd_length>
  <client_account_creation_disabled/>
  <terms_of_use>  Be
     nice.  </terms_of_use>
  <platforms>
    <platform><platform_name>windows_x86_64</platform_name><user_friendly_name>Windows</user_friendly_name></platform>
    <platform><platform_name>x86_64-pc-linux-gnu</platform_name></platform>
  </platforms>
</project_config>`

func TestParseConfig(t *testing.T) {
	c, err := ParseConfig(strings.NewReader(configXML))
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "Einstein@Home" || c.MinPasswordLength != 6 || !c.AccountCreationDisabled {
		t.Errorf("config = %+v", c)
	}
	if c.TermsOfUse != "Be nice." {
		t.Errorf("terms = %q", c.TermsOfUse)
	}
	if strings.Join(c.Platforms, ",") != "windows_x86_64,x86_64-pc-linux-gnu" {
		t.Errorf("platforms = %v", c.Platforms)
	}
}

func TestParseConfigOpenSignup(t *testing.T) {
	c, err := ParseConfig(strings.NewReader(`<project_config><name>P</name><master_url>https://p/</master_url></project_config>`))
	if err != nil || c.AccountCreationDisabled {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestParseConfigRejectsNonProjects(t *testing.T) {
	for name, body := range map[string]string{
		"html":      "<html><body>Parked domain</body></html>",
		"empty xml": "<project_config></project_config>",
		"error":     "<project_config><error_num>-1</error_num><error_msg>down</error_msg></project_config>",
		"garbage":   "not xml at all",
	} {
		if _, err := ParseConfig(strings.NewReader(body)); err == nil {
			t.Errorf("%s: should be rejected", name)
		}
	}
}

func TestFetchConfig(t *testing.T) {
	srv := serve(t, configXML, 200)
	c, err := FetchConfig(context.Background(), srv.URL)
	if err != nil || c.Name != "Einstein@Home" {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := FetchConfig(context.Background(), serve(t, "", 404).URL); err == nil {
		t.Error("HTTP 404 must be an error")
	}
	for _, bad := range []string{"", "ftp://x.example/", "javascript:alert(1)", "file:///etc/passwd"} {
		if _, err := FetchConfig(context.Background(), bad); err == nil {
			t.Errorf("%q must be refused before any request is made", bad)
		}
	}
}
