package detect

import (
	"fmt"
	"os"
	"strings"
)

// The Linux kernel describes the CPU differently on every architecture:
// x86 has vendor_id/model name, 32-bit ARM has model name/Hardware, arm64 only
// numeric implementer/part codes, RISC-V uarch/isa, PowerPC cpu/machine.
// parseCPUInfo turns any of them into a vendor and a readable model.

var armVendors = map[string]string{
	"0x41": "ARM", "0x42": "Broadcom", "0x43": "Cavium", "0x44": "DEC", "0x46": "Fujitsu",
	"0x48": "HiSilicon", "0x49": "Infineon", "0x4d": "Motorola", "0x4e": "NVIDIA",
	"0x50": "Applied Micro", "0x51": "Qualcomm", "0x53": "Samsung", "0x56": "Marvell",
	"0x61": "Apple", "0x66": "Faraday", "0x69": "Intel", "0xc0": "Ampere",
}

// armParts names the common ARM Ltd. cores (implementer 0x41).
var armParts = map[string]string{
	"0xd03": "Cortex-A53", "0xd04": "Cortex-A35", "0xd05": "Cortex-A55", "0xd07": "Cortex-A57",
	"0xd08": "Cortex-A72", "0xd09": "Cortex-A73", "0xd0a": "Cortex-A75", "0xd0b": "Cortex-A76",
	"0xd0c": "Neoverse-N1", "0xd0d": "Cortex-A77", "0xd40": "Neoverse-V1", "0xd41": "Cortex-A78",
	"0xd44": "Cortex-X1", "0xd46": "Cortex-A510", "0xd47": "Cortex-A710", "0xd49": "Neoverse-N2",
	"0xd4d": "Cortex-A715", "0xd4f": "Neoverse-V2", "0xd80": "Cortex-A520", "0xd81": "Cortex-A720",
}

var riscvVendors = map[string]string{
	"sifive": "SiFive", "thead": "T-Head", "starfive": "StarFive", "spacemit": "SpacemiT",
	"andestech": "Andes", "canaan": "Canaan", "sophgo": "Sophgo", "ultrarisc": "UltraRISC",
}

func parseCPUInfo(text string) (vendor, model string) {
	kv := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		i := strings.Index(line, ":")
		if i < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:i]))
		val := strings.TrimSpace(line[i+1:])
		if _, seen := kv[key]; !seen && val != "" { // the first core wins
			kv[key] = val
		}
	}

	switch {
	case kv["vendor_id"] != "": // x86
		vendor, model = kv["vendor_id"], kv["model name"]

	case kv["cpu implementer"] != "": // ARM, 32- and 64-bit
		impl := strings.ToLower(kv["cpu implementer"])
		vendor = armVendors[impl]
		if vendor == "" {
			vendor = "ARM"
		}
		model = kv["model name"]
		if model == "" {
			part := strings.ToLower(kv["cpu part"])
			if name := armParts[part]; name != "" && impl == "0x41" {
				model = name
			} else if part != "" {
				model = fmt.Sprintf("%s CPU part %s", vendor, part)
			}
		}
		if board := firstNonEmpty(kv["model"], kv["hardware"]); board != "" && !strings.Contains(model, board) {
			model = strings.TrimSpace(model + " (" + board + ")")
		}

	case kv["isa"] != "" || kv["uarch"] != "": // RISC-V
		uarch := kv["uarch"]
		vendor = "RISC-V"
		if uarch != "" {
			if v, _, ok := strings.Cut(uarch, ","); ok && v != "" {
				if name := riscvVendors[strings.ToLower(v)]; name != "" {
					vendor = name
				} else {
					vendor = v
				}
			}
			model = uarch
		}
		if isa := kv["isa"]; isa != "" {
			// Keep the base ISA (rv64imafdc); the extension list after the first
			// underscore can run to hundreds of characters.
			isa, _, _ = strings.Cut(isa, "_")
			if model == "" {
				model = isa
			} else {
				model += " (" + isa + ")"
			}
		}
		if board := kv["model"]; board != "" && !strings.Contains(model, board) {
			model += " " + board
		}

	case kv["cpu"] != "": // PowerPC
		model, _, _ = strings.Cut(kv["cpu"], ",")
		model = strings.TrimSpace(model)
		up := strings.ToUpper(model)
		if strings.HasPrefix(up, "POWER") || strings.HasPrefix(up, "PPC970") {
			vendor = "IBM"
		}
		if m := firstNonEmpty(kv["machine"], kv["model"]); m != "" {
			model += " (" + m + ")"
		}

	default:
		model = firstNonEmpty(kv["model name"], kv["hardware"], kv["model"])
	}
	return vendor, strings.TrimSpace(model)
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// parseMemTotal reads MemTotal (in bytes) from the text of /proc/meminfo.
func parseMemTotal(text string) float64 {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				var kb float64
				fmt.Sscanf(f[1], "%f", &kb)
				return kb * 1024
			}
		}
	}
	return 0
}

// deviceTreeModel is the board name single-board computers publish
// (Raspberry Pi, VisionFive, ...).
func deviceTreeModel() string {
	b, err := os.ReadFile("/proc/device-tree/model")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.Trim(string(b), "\x00"))
}

// kernelRelease is the running Linux kernel version, e.g. "6.6.31-v8+".
func kernelRelease() string {
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
