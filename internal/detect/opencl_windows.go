//go:build windows && (amd64 || arm64)

package detect

import (
	"strings"
	"syscall"
	"unsafe"
)

// Talks to the installed OpenCL runtime (OpenCL.dll) directly, without cgo, so
// the client stays a single static binary.

var (
	clDLL           = syscall.NewLazyDLL("OpenCL.dll")
	pGetPlatformIDs = clDLL.NewProc("clGetPlatformIDs")
	pGetPlatformInf = clDLL.NewProc("clGetPlatformInfo")
	pGetDeviceIDs   = clDLL.NewProc("clGetDeviceIDs")
	pGetDeviceInfo  = clDLL.NewProc("clGetDeviceInfo")
)

// OpenCL constants (CL/cl.h and the vendor extension headers).
const (
	clDeviceTypeGPU = 1 << 2

	clPlatformVersion = 0x0901

	clDeviceMaxComputeUnits   = 0x1002
	clDeviceMaxClockFrequency = 0x100C
	clDeviceVendorID          = 0x1001
	clDeviceSingleFPConfig    = 0x101B
	clDeviceGlobalMemSize     = 0x101F
	clDeviceLocalMemSize      = 0x1023
	clDeviceAvailable         = 0x1027
	clDeviceEndianLittle      = 0x1026
	clDeviceExecCaps          = 0x1029
	clDeviceName              = 0x102B
	clDeviceVendor            = 0x102C
	clDriverVersion           = 0x102D
	clDeviceVersion           = 0x102F
	clDeviceExtensions        = 0x1030
	clDeviceDoubleFPConfig    = 0x1032
	clDeviceHalfFPConfig      = 0x1033
	clNvCCMajor               = 0x4000
	clNvCCMinor               = 0x4001
	clAmdSimdPerCU            = 0x4040
	clAmdSimdWidth            = 0x4041
	clAmdSimdInstrWidth       = 0x4042
)

func openCLDevices() (out []OpenCLDevice) {
	defer func() {
		if recover() != nil {
			out = nil
		}
	}()
	if clDLL.Load() != nil {
		return nil
	}
	var n uint32
	if r, _, _ := pGetPlatformIDs.Call(0, 0, uintptr(unsafe.Pointer(&n))); r != 0 || n == 0 {
		return nil
	}
	plats := make([]uintptr, n)
	if r, _, _ := pGetPlatformIDs.Call(uintptr(n), uintptr(unsafe.Pointer(&plats[0])), 0); r != 0 {
		return nil
	}
	for _, p := range plats {
		pver := clInfoString(pGetPlatformInf, p, clPlatformVersion)
		var nd uint32
		if r, _, _ := pGetDeviceIDs.Call(p, clDeviceTypeGPU, 0, 0, uintptr(unsafe.Pointer(&nd))); r != 0 || nd == 0 {
			continue
		}
		devs := make([]uintptr, nd)
		if r, _, _ := pGetDeviceIDs.Call(p, clDeviceTypeGPU, uintptr(nd), uintptr(unsafe.Pointer(&devs[0])), 0); r != 0 {
			continue
		}
		for _, d := range devs {
			dev := OpenCLDevice{
				Name:            clInfoString(pGetDeviceInfo, d, clDeviceName),
				Vendor:          clInfoString(pGetDeviceInfo, d, clDeviceVendor),
				VendorID:        clInfoUint(d, clDeviceVendorID),
				Available:       clInfoUint(d, clDeviceAvailable) != 0,
				HalfFPConfig:    clInfoUint(d, clDeviceHalfFPConfig),
				SingleFPConfig:  clInfoUint(d, clDeviceSingleFPConfig),
				DoubleFPConfig:  clInfoUint(d, clDeviceDoubleFPConfig),
				EndianLittle:    clInfoUint(d, clDeviceEndianLittle) != 0,
				ExecutionCaps:   clInfoUint(d, clDeviceExecCaps),
				Extensions:      clInfoString(pGetDeviceInfo, d, clDeviceExtensions),
				GlobalMem:       clInfoUint(d, clDeviceGlobalMemSize),
				LocalMem:        clInfoUint(d, clDeviceLocalMemSize),
				MaxClockMHz:     clInfoUint(d, clDeviceMaxClockFrequency),
				MaxComputeUnits: clInfoUint(d, clDeviceMaxComputeUnits),
				PlatformVersion: pver,
				DeviceVersion:   clInfoString(pGetDeviceInfo, d, clDeviceVersion),
				DriverVersion:   clInfoString(pGetDeviceInfo, d, clDriverVersion),
			}
			if strings.Contains(strings.ToLower(dev.Vendor), "nvidia") {
				dev.NvCCMajor = clInfoUint(d, clNvCCMajor)
				dev.NvCCMinor = clInfoUint(d, clNvCCMinor)
			}
			if k := dev.Kind(); k == "amd" {
				dev.AmdSimdPerCU = clInfoUint(d, clAmdSimdPerCU)
				dev.AmdSimdWidth = clInfoUint(d, clAmdSimdWidth)
				dev.AmdSimdInstrWidth = clInfoUint(d, clAmdSimdInstrWidth)
			}
			out = append(out, dev)
		}
	}
	return out
}

// clInfoString reads a string-valued platform/device property.
func clInfoString(proc *syscall.LazyProc, obj uintptr, param uintptr) string {
	var size uintptr
	if r, _, _ := proc.Call(obj, param, 0, 0, uintptr(unsafe.Pointer(&size))); r != 0 || size == 0 || size > 1<<16 {
		return ""
	}
	buf := make([]byte, size)
	if r, _, _ := proc.Call(obj, param, size, uintptr(unsafe.Pointer(&buf[0])), 0); r != 0 {
		return ""
	}
	return strings.TrimRight(string(buf), "\x00 ")
}

// clInfoUint reads an integer-valued device property of any width up to 64 bits.
func clInfoUint(dev uintptr, param uintptr) uint64 {
	var v [8]byte
	var got uintptr
	if r, _, _ := pGetDeviceInfo.Call(dev, param, 8, uintptr(unsafe.Pointer(&v[0])), uintptr(unsafe.Pointer(&got))); r != 0 || got == 0 || got > 8 {
		return 0
	}
	var x uint64
	for i := int(got) - 1; i >= 0; i-- {
		x = x<<8 | uint64(v[i])
	}
	return x
}
