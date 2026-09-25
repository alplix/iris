//go:build !windows

package worker

import (
	"os"
	"path/filepath"
	"syscall"
)

// mmapFileName is where a Unix/macOS application's BOINC API looks for the
// segment: a memory-mapped file in its working (slot) directory, selected by a
// shm_key of -1 in init_data.xml (lib/app_ipc.h MMAPPED_FILE_NAME).
const mmapFileName = "boinc_mmap_file"

func createShmem(slotDir string) (*shmem, error) {
	path := filepath.Join(slotDir, mmapFileName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	if err := f.Truncate(shmSize); err != nil {
		f.Close()
		return nil, err
	}
	mem, err := syscall.Mmap(int(f.Fd()), 0, shmSize, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		f.Close()
		return nil, err
	}
	s := newShmemOver(mem, "<shm_key>-1</shm_key>", func() {
		syscall.Munmap(mem)
		f.Close()
	})
	s.reset()
	return s, nil
}
