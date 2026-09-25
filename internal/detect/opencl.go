package detect

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"time"
)

// OpenCLDevice is one GPU as the OpenCL driver describes it; these are the
// fields a BOINC scheduler reads from <coproc_opencl> to decide whether an
// OpenCL application version can run on the machine.
type OpenCLDevice struct {
	Name              string `json:"name"`
	Vendor            string `json:"vendor"`
	VendorID          uint64 `json:"vendorId"`
	Available         bool   `json:"available"`
	HalfFPConfig      uint64 `json:"halfFp"`
	SingleFPConfig    uint64 `json:"singleFp"`
	DoubleFPConfig    uint64 `json:"doubleFp"`
	EndianLittle      bool   `json:"endianLittle"`
	ExecutionCaps     uint64 `json:"execCaps"`
	Extensions        string `json:"extensions"`
	GlobalMem         uint64 `json:"globalMem"`
	LocalMem          uint64 `json:"localMem"`
	MaxClockMHz       uint64 `json:"maxClock"`
	MaxComputeUnits   uint64 `json:"computeUnits"`
	NvCCMajor         uint64 `json:"nvCcMajor"`
	NvCCMinor         uint64 `json:"nvCcMinor"`
	AmdSimdPerCU      uint64 `json:"amdSimdPerCu"`
	AmdSimdWidth      uint64 `json:"amdSimdWidth"`
	AmdSimdInstrWidth uint64 `json:"amdSimdInstrWidth"`
	PlatformVersion   string `json:"platformVersion"`
	DeviceVersion     string `json:"deviceVersion"`
	DriverVersion     string `json:"driverVersion"`
}

// Kind classifies a device by vendor: "nvidia", "amd", "intel", "apple" or "other".
func (d OpenCLDevice) Kind() string {
	v := strings.ToLower(d.Vendor + " " + d.Name)
	switch {
	case strings.Contains(v, "nvidia"):
		return "nvidia"
	case strings.Contains(strings.ToLower(d.Vendor), "apple"):
		return "apple"
	case strings.Contains(v, "advanced micro") || strings.Contains(v, "amd") || strings.Contains(v, "ati "):
		return "amd"
	case strings.Contains(v, "intel"):
		return "intel"
	}
	return "other"
}

// ProbeArg is the command-line argument that makes the client print the OpenCL
// devices as JSON and exit (see OpenCLViaProbe).
const ProbeArg = "--probe-opencl"

// OpenCLViaProbe lists the OpenCL GPUs by running `exe --probe-opencl` as a
// separate process. The driver's OpenCL library is native code Iris does not
// control; if it misbehaves it takes down only the probe, never the client.
// Any failure means "none found".
func OpenCLViaProbe(exe string) []OpenCLDevice {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, ProbeArg).Output()
	if err != nil {
		return nil
	}
	return ParseProbeOutput(out)
}

// ParseProbeOutput reads what ProbeOpenCL printed.
func ParseProbeOutput(out []byte) []OpenCLDevice {
	var devs []OpenCLDevice
	if json.Unmarshal(out, &devs) != nil {
		return nil
	}
	var ok []OpenCLDevice
	for _, d := range devs {
		if strings.TrimSpace(d.Name) != "" {
			ok = append(ok, d)
		}
	}
	return ok
}

// ProbeOpenCL is what the probe process runs: query the driver and print JSON.
func ProbeOpenCL() []byte {
	devs := openCLDevices()
	if devs == nil {
		devs = []OpenCLDevice{}
	}
	b, _ := json.Marshal(devs)
	return b
}
