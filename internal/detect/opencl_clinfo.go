package detect

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Where Iris cannot load the OpenCL library itself (no cgo), it asks the
// `clinfo` tool, if the machine has one (packages "clinfo" on Linux, Homebrew's
// on macOS). `clinfo --raw` prints one "[P<platform>/<device>] CL_... value"
// line per property.

var clinfoLineRe = regexp.MustCompile(`^\[P(\d+)/(\d+|\*)\]\s+(CL_[A-Z0-9_]+)\s*(.*)$`)
var clinfoPlatformRe = regexp.MustCompile(`^\s{2}(CL_PLATFORM_[A-Z]+)\s+(.*)$`)

// parseClinfoRaw reads `clinfo --raw` output into the GPU devices it lists.
func parseClinfoRaw(text string) []OpenCLDevice {
	props := map[string]map[string]string{} // "p/d" -> property -> value
	var order []string
	platVersion := map[string]string{}
	curPlat := -1
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if m := clinfoLineRe.FindStringSubmatch(line); m != nil {
			if m[2] == "*" {
				continue
			}
			key := m[1] + "/" + m[2]
			if props[key] == nil {
				props[key] = map[string]string{}
				order = append(order, key)
			}
			props[key][m[3]] = strings.TrimSpace(m[4])
			continue
		}
		// The header block before the devices: platform properties, unprefixed.
		if m := clinfoPlatformRe.FindStringSubmatch(line); m != nil && m[1] == "CL_PLATFORM_VERSION" {
			curPlat++
			platVersion[strconv.Itoa(curPlat)] = strings.TrimSpace(m[2])
		}
	}
	var out []OpenCLDevice
	for _, key := range order {
		p := props[key]
		if !strings.Contains(p["CL_DEVICE_TYPE"], "GPU") || strings.TrimSpace(p["CL_DEVICE_NAME"]) == "" {
			continue
		}
		plat := strings.SplitN(key, "/", 2)[0]
		d := OpenCLDevice{
			Name: p["CL_DEVICE_NAME"], Vendor: p["CL_DEVICE_VENDOR"],
			VendorID:          parseUintAny(p["CL_DEVICE_VENDOR_ID"]),
			Available:         p["CL_DEVICE_AVAILABLE"] == "CL_TRUE",
			HalfFPConfig:      fpConfig(p["CL_DEVICE_HALF_FP_CONFIG"]),
			SingleFPConfig:    fpConfig(p["CL_DEVICE_SINGLE_FP_CONFIG"]),
			DoubleFPConfig:    fpConfig(p["CL_DEVICE_DOUBLE_FP_CONFIG"]),
			EndianLittle:      p["CL_DEVICE_ENDIAN_LITTLE"] == "CL_TRUE",
			ExecutionCaps:     execCaps(p["CL_DEVICE_EXECUTION_CAPABILITIES"]),
			Extensions:        p["CL_DEVICE_EXTENSIONS"],
			GlobalMem:         parseUintAny(p["CL_DEVICE_GLOBAL_MEM_SIZE"]),
			LocalMem:          parseUintAny(p["CL_DEVICE_LOCAL_MEM_SIZE"]),
			MaxClockMHz:       parseUintAny(p["CL_DEVICE_MAX_CLOCK_FREQUENCY"]),
			MaxComputeUnits:   parseUintAny(p["CL_DEVICE_MAX_COMPUTE_UNITS"]),
			NvCCMajor:         parseUintAny(p["CL_DEVICE_COMPUTE_CAPABILITY_MAJOR_NV"]),
			NvCCMinor:         parseUintAny(p["CL_DEVICE_COMPUTE_CAPABILITY_MINOR_NV"]),
			AmdSimdPerCU:      parseUintAny(p["CL_DEVICE_SIMD_PER_COMPUTE_UNIT_AMD"]),
			AmdSimdWidth:      parseUintAny(p["CL_DEVICE_SIMD_WIDTH_AMD"]),
			AmdSimdInstrWidth: parseUintAny(p["CL_DEVICE_SIMD_INSTRUCTION_WIDTH_AMD"]),
			PlatformVersion:   platVersion[plat],
			DeviceVersion:     strings.TrimSpace(p["CL_DEVICE_VERSION"]),
			DriverVersion:     p["CL_DRIVER_VERSION"],
		}
		out = append(out, d)
	}
	return out
}

func parseUintAny(s string) uint64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if strings.HasPrefix(s, "0x") {
		v, _ := strconv.ParseUint(s[2:], 16, 64)
		return v
	}
	v, _ := strconv.ParseUint(s, 10, 64)
	return v
}

var fpBits = map[string]uint64{
	"CL_FP_DENORM": 1, "CL_FP_INF_NAN": 2, "CL_FP_ROUND_TO_NEAREST": 4, "CL_FP_ROUND_TO_ZERO": 8,
	"CL_FP_ROUND_TO_INF": 16, "CL_FP_FMA": 32, "CL_FP_SOFT_FLOAT": 64, "CL_FP_CORRECTLY_ROUNDED_DIVIDE_SQRT": 128,
}

// fpConfig turns clinfo's "CL_FP_INF_NAN | CL_FP_FMA" back into the bit field.
func fpConfig(s string) uint64 {
	var v uint64
	for _, name := range strings.Split(s, "|") {
		v |= fpBits[strings.TrimSpace(name)]
	}
	return v
}

func execCaps(s string) uint64 {
	var v uint64
	if strings.Contains(s, "CL_EXEC_KERNEL") {
		v |= 1
	}
	if strings.Contains(s, "CL_EXEC_NATIVE_KERNEL") {
		v |= 2
	}
	return v
}

// clinfoDevices runs `clinfo --raw`; no tool, no devices.
func clinfoDevices() []OpenCLDevice {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "clinfo", "--raw").Output()
	if err != nil {
		return nil
	}
	return parseClinfoRaw(string(out))
}
