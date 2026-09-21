package scheduler

import "runtime"

// Platform is the BOINC platform name reported to project schedulers so they
// hand out application versions that can run on this machine.
func Platform() string {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "windows/amd64":
		return "x86_64-pc-windows-gnu"
	case "windows/arm64":
		return "aarch64-pc-windows-gnu"
	case "linux/amd64":
		return "x86_64-pc-linux-gnu"
	case "linux/arm64":
		return "aarch64-unknown-linux-gnu"
	case "darwin/amd64":
		return "x86_64-apple-darwin"
	case "darwin/arm64":
		return "arm64-apple-darwin"
	}
	return runtime.GOARCH + "-" + runtime.GOOS
}
