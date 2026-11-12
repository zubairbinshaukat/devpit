package extract

import (
	"io/fs"
	"os"
)

// osFS is the real StatFS: Lstat, and folder names read from a directory
// handle. It never opens a file (rule 78): spike X1 showed listing names
// while hatchzip renames part files never makes its rename fail.
type osFS struct{}

func (osFS) Lstat(name string) (fs.FileInfo, error) { return os.Lstat(name) }

func (osFS) ReadDirNames(dir string) ([]string, error) {
	fi, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, &fs.PathError{Op: "readdir", Path: dir, Err: fs.ErrInvalid}
	}
	d, err := os.Open(dir) // #nosec G304 -- a directory (checked by Lstat above), opened for names only
	if err != nil {
		return nil, err
	}
	defer func() { _ = d.Close() }()
	return d.Readdirnames(-1)
}
