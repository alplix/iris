// Package fsutil holds small file helpers shared by the packages that keep
// the person's data on disk.
package fsutil

import "os"

// WriteFile replaces path so that a crash or power loss at any moment leaves
// either the complete old file or the complete new one, never a truncated mix
// (os.WriteFile truncates first, so an interruption loses the file). With
// keepBackup the file being replaced is kept as path+".bak".
func WriteFile(path string, data []byte, perm os.FileMode, keepBackup bool) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if keepBackup {
		if _, err := os.Stat(path); err == nil {
			os.Rename(path, path+".bak")
		}
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
