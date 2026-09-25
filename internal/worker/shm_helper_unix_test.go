//go:build !windows

package worker

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

// attachAsApp is the application side of the channel: a shm_key of -1 means a
// memory-mapped file called boinc_mmap_file in the working directory
// (api/boinc_api.cpp setup_shared_mem).
func attachAsApp(initData string) ([]byte, error) {
	if !strings.Contains(initData, "<shm_key>-1</shm_key>") {
		return nil, fmt.Errorf("no shm_key -1 in init_data.xml")
	}
	f, err := os.OpenFile("boinc_mmap_file", os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	return syscall.Mmap(int(f.Fd()), 0, shmSize, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
}
