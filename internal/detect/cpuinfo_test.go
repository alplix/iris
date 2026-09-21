package detect

import (
	"strings"
	"testing"
)

// Trimmed /proc/cpuinfo captures from real machines of each architecture.
const (
	cpuinfoX86 = "processor\t: 0\nvendor_id\t: GenuineIntel\nmodel name\t: 13th Gen Intel(R) Core(TM) i5-13400F\n\nprocessor\t: 1\nvendor_id\t: GenuineIntel\nmodel name\t: other core\n"

	cpuinfoPi3 = "processor\t: 0\nmodel name\t: ARMv7 Processor rev 4 (v7l)\nBogoMIPS\t: 38.40\nFeatures\t: half thumb fastmult vfp edsp neon vfpv3\nCPU implementer\t: 0x41\nCPU architecture: 7\nCPU variant\t: 0x0\nCPU part\t: 0xd03\nCPU revision\t: 4\n\nHardware\t: BCM2835\nRevision\t: a02082\nSerial\t\t: 00000000deadbeef\nModel\t\t: Raspberry Pi 3 Model B Rev 1.2\n"

	cpuinfoPi4 = "processor\t: 0\nBogoMIPS\t: 108.00\nFeatures\t: fp asimd evtstrm crc32 cpuid\nCPU implementer\t: 0x41\nCPU architecture: 8\nCPU variant\t: 0x0\nCPU part\t: 0xd08\nCPU revision\t: 3\n\nHardware\t: BCM2835\nModel\t\t: Raspberry Pi 4 Model B Rev 1.4\n"

	cpuinfoAmpere = "processor\t: 0\nBogoMIPS\t: 50.00\nCPU implementer\t: 0x41\nCPU architecture: 8\nCPU variant\t: 0x3\nCPU part\t: 0xd0c\nCPU revision\t: 1\n"

	cpuinfoUnknownARM = "processor\t: 0\nCPU implementer\t: 0x51\nCPU part\t: 0x001\n"

	cpuinfoRISCV = "processor\t: 0\nhart\t\t: 1\nisa\t\t: rv64imafdc_zicntr_zicsr_zifencei_zihpm\nmmu\t\t: sv39\nuarch\t\t: sifive,u74-mc\nmvendorid\t: 0x489\nmarchid\t\t: 0x8000000000000007\n"

	cpuinfoPOWER9 = "processor\t: 0\ncpu\t\t: POWER9 (architected), altivec supported\nclock\t\t: 3800.000000MHz\nrevision\t: 2.2 (pvr 004e 1202)\n\ntimebase\t: 512000000\nplatform\t: PowerNV\nmodel\t\t: 9006-22P\nmachine\t\t: PowerNV 9006-22P\n"

	cpuinfoG5 = "processor\t: 0\ncpu\t\t: PPC970FX, altivec supported\nclock\t\t: 2000.000000MHz\nrevision\t: 3.0 (pvr 003c 0300)\n\nmachine\t\t: PowerMac7,3\n"
)

func TestParseCPUInfoPerArchitecture(t *testing.T) {
	cases := []struct {
		name, text, vendor string
		model              []string // every fragment must appear in the model
	}{
		{"x86", cpuinfoX86, "GenuineIntel", []string{"i5-13400F"}},
		{"arm 32-bit (Pi 3)", cpuinfoPi3, "ARM", []string{"ARMv7", "Raspberry Pi 3 Model B"}},
		{"arm64 (Pi 4)", cpuinfoPi4, "ARM", []string{"Cortex-A72", "Raspberry Pi 4 Model B"}},
		{"arm64 server", cpuinfoAmpere, "ARM", []string{"Neoverse-N1"}},
		{"arm unknown part", cpuinfoUnknownARM, "Qualcomm", []string{"0x001"}},
		{"riscv64", cpuinfoRISCV, "SiFive", []string{"sifive,u74-mc", "rv64imafdc"}},
		{"ppc64le", cpuinfoPOWER9, "IBM", []string{"POWER9", "PowerNV 9006-22P"}},
		{"ppc (G5)", cpuinfoG5, "IBM", []string{"PPC970FX", "PowerMac7,3"}},
	}
	for _, c := range cases {
		vendor, model := parseCPUInfo(c.text)
		if vendor != c.vendor {
			t.Errorf("%s: vendor = %q, want %q", c.name, vendor, c.vendor)
		}
		for _, frag := range c.model {
			if !strings.Contains(model, frag) {
				t.Errorf("%s: model %q lacks %q", c.name, model, frag)
			}
		}
		if model == "" {
			t.Errorf("%s: empty model", c.name)
		}
	}
}

func TestParseCPUInfoRISCVDropsTheExtensionList(t *testing.T) {
	_, model := parseCPUInfo("isa\t: rv64imafdcbvh_zic64b_zicbom_zicbop_zicboz_zicond_zk_zvbb\nmmu\t: sv57\n")
	if strings.Contains(model, "zic") || !strings.Contains(model, "rv64imafdcbvh") {
		t.Fatalf("model %q should keep only the base ISA", model)
	}
}

func TestParseCPUInfoUsesTheFirstCoreOnly(t *testing.T) {
	if _, model := parseCPUInfo(cpuinfoX86); strings.Contains(model, "other core") {
		t.Fatalf("model %q must come from the first core", model)
	}
}

func TestParseCPUInfoUnknownIsEmptyNotAPanic(t *testing.T) {
	for _, s := range []string{"", "garbage", "no colon here\n\n", ":\n: x"} {
		v, m := parseCPUInfo(s)
		if v != "" || m != "" {
			t.Errorf("parseCPUInfo(%q) = %q, %q; want empty", s, v, m)
		}
	}
}

func TestParseMemTotal(t *testing.T) {
	if got := parseMemTotal("MemTotal:       16384000 kB\nMemFree: 1 kB\n"); got != 16384000*1024 {
		t.Errorf("MemTotal = %v", got)
	}
	if got := parseMemTotal("nothing"); got != 0 {
		t.Errorf("no MemTotal should give 0, got %v", got)
	}
}

func TestEstimateFLOPSOnlyForKnownCPUs(t *testing.T) {
	if f, known := estimateFLOPS(8, "Intel(R) Core(TM) i9-13900K"); !known || f <= 0 {
		t.Errorf("a listed CPU should be estimated, got %v %v", f, known)
	}
	for _, m := range []string{"", "Cortex-A53 (Raspberry Pi 3)", "sifive,u74-mc (rv64imafdc)", "POWER9"} {
		if _, known := estimateFLOPS(4, m); known {
			t.Errorf("%q must be measured, not guessed", m)
		}
	}
}
