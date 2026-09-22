package wt

import (
	"os"
	"path/filepath"
)

// writeFileAtomic writes data to a temp file in filepath.Dir(dest) and
// renames it over dest, so Windows Terminal's file watcher (or a crash)
// never observes a partially written settings.json.
func writeFileAtomic(dest string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(dest)
	tmp, err := os.CreateTemp(dir, ".devpit-wt-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename below succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	return os.Rename(tmpName, dest)
}
