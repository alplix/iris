package detect

import "testing"

func TestWindowsOSReadsLikeTheReferenceClient(t *testing.T) {
	reg := "\r\nHKEY_LOCAL_MACHINE\\SOFTWARE\\Microsoft\\Windows NT\\CurrentVersion\r\n    ProductName    REG_SZ    Windows 10 Pro\r\n    CurrentBuildNumber    REG_SZ    26200\r\n    UBR    REG_DWORD    0x1382\r\n"
	name, ver := windowsOS(parseRegQuery(reg), "amd64")
	if name != "Microsoft Windows 11 Pro" || ver != "10.0.26200.4994 x64" {
		t.Errorf("got %q %q", name, ver)
	}
	if n, v := windowsOS(map[string]string{}, "amd64"); n != "" || v != "" {
		t.Error("an unreadable registry must give nothing so the fallback is kept")
	}
}

func TestParseOSRelease(t *testing.T) {
	n, v := parseOSRelease("NAME=\"Ubuntu\"\nVERSION=\"26.04 LTS\"\nID=ubuntu\n")
	if n != "Ubuntu" || v != "26.04 LTS" {
		t.Errorf("got %q %q", n, v)
	}
}
