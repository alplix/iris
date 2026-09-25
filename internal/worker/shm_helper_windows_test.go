//go:build windows

package worker

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

// attachAsApp is the application side of the channel, written independently of
// the client's code the way the BOINC API does it (lib/shmem.cpp attach_shmem).
func attachAsApp(initData string) ([]byte, error) {
	i := strings.Index(initData, "<comm_obj_name>")
	j := strings.Index(initData, "</comm_obj_name>")
	if i < 0 || j < 0 {
		return nil, fmt.Errorf("no comm_obj_name in init_data.xml")
	}
	name, _ := syscall.UTF16PtrFromString("shm_" + initData[i+len("<comm_obj_name>"):j])
	k := syscall.NewLazyDLL("kernel32.dll")
	h, _, err := k.NewProc("OpenFileMappingW").Call(0x000F001F, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return nil, fmt.Errorf("OpenFileMapping: %v", err)
	}
	view, _, err := k.NewProc("MapViewOfFile").Call(h, 0x000F001F, 0, 0, 0)
	if view == 0 {
		return nil, fmt.Errorf("MapViewOfFile: %v", err)
	}
	base := *(*unsafe.Pointer)(unsafe.Pointer(&view))
	return unsafe.Slice((*byte)(base), shmSize), nil
}
