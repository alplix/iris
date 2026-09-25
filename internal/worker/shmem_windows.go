//go:build windows

package worker

import (
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	pCreateFileMapping = kernel32.NewProc("CreateFileMappingW")
	pMapViewOfFile     = kernel32.NewProc("MapViewOfFile")
	pUnmapViewOfFile   = kernel32.NewProc("UnmapViewOfFile")
)

const (
	pageReadWrite  = 0x04
	fileMapAllAcc  = 0x000F001F
	invalidHandle  = ^uintptr(0)
	errAlreadyExst = 183
)

// createShmem creates the task's segment as a named file mapping,
// "shm_boinc_<slot>", which is where the BOINC API of a Windows application
// looks for it (lib/shmem.cpp attach_shmem; the name comes from init_data.xml's
// comm_obj_name, "boinc_<slot>").
func createShmem(slotDir string) (*shmem, error) {
	commName := "boinc_" + strings.TrimLeft(filepath.Base(slotDir), "\\/")
	name, err := syscall.UTF16PtrFromString("shm_" + commName)
	if err != nil {
		return nil, err
	}
	h, _, callErr := pCreateFileMapping.Call(invalidHandle, 0, pageReadWrite, 0, shmSize, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return nil, fmt.Errorf("CreateFileMapping: %v", callErr)
	}
	if callErr == syscall.Errno(errAlreadyExst) {
		// A leftover from an earlier task in this slot; start from a clean one.
		syscall.CloseHandle(syscall.Handle(h))
		return nil, fmt.Errorf("segment %s already exists", commName)
	}
	view, _, callErr := pMapViewOfFile.Call(h, fileMapAllAcc, 0, 0, 0)
	if view == 0 {
		syscall.CloseHandle(syscall.Handle(h))
		return nil, fmt.Errorf("MapViewOfFile: %v", callErr)
	}
	// view is an address handed out by the kernel, not Go memory.
	base := *(*unsafe.Pointer)(unsafe.Pointer(&view))
	mem := unsafe.Slice((*byte)(base), shmSize)
	s := newShmemOver(mem, "<comm_obj_name>"+commName+"</comm_obj_name>", func() {
		pUnmapViewOfFile.Call(view)
		syscall.CloseHandle(syscall.Handle(h))
	})
	s.reset()
	return s, nil
}
