package scheduler

import "runtime"

// platforms maps GOOS/GOARCH to the BOINC platform names project servers know.
// Work is only handed out when a project has an application for the platform
// (32-bit ARM, RISC-V and POWER have few or none; the project decides).
var platforms = map[string]string{
	"windows/amd64": "x86_64-pc-windows-gnu",
	"windows/386":   "i686-pc-windows-gnu",
	"windows/arm64": "aarch64-pc-windows-gnu",
	"linux/amd64":   "x86_64-pc-linux-gnu",
	"linux/386":     "i686-pc-linux-gnu",
	"linux/arm":     "arm-unknown-linux-gnueabihf",
	"linux/arm64":   "aarch64-unknown-linux-gnu",
	"linux/riscv64": "riscv64-unknown-linux-gnu",
	"linux/ppc64le": "powerpc64le-unknown-linux-gnu",
	"linux/ppc64":   "powerpc64-unknown-linux-gnu",
	"darwin/amd64":  "x86_64-apple-darwin",
	"darwin/arm64":  "arm64-apple-darwin",
}

// Platform is the BOINC platform name reported to project schedulers so they
// hand out application versions that can run on this machine.
func Platform() string {
	if p, ok := platforms[runtime.GOOS+"/"+runtime.GOARCH]; ok {
		return p
	}
	return runtime.GOARCH + "-" + runtime.GOOS
}
