//go:build !windows

package clean

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// resolvesThroughReparse is the non-Windows form of the reparse-point check.
// There are no junctions here, so the equivalent question is whether the path
// or any ancestor is a symlink or another irregular file, which Go reports in
// the file mode.
func resolvesThroughReparse(p string) (bool, error) {
	cur := filepath.Clean(p)
	for {
		info, err := os.Lstat(cur)
		switch {
		case err == nil:
			if info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
				return true, nil
			}
		case errors.Is(err, fs.ErrNotExist):
			// Keep climbing; an ancestor may still be a link.
		default:
			return false, err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return false, nil
		}
		cur = parent
	}
}

// isNetworkPath is Windows-only; UNC paths are caught by a string test that
// runs everywhere.
func isNetworkPath(string) bool { return false }

// isLocked is Windows-only. Nothing outside Windows refuses a rename because a
// file is open.
func isLocked(error) bool { return false }

// longPath returns the path unchanged; the extended-length prefix is a Windows
// idea.
func longPath(p string) string { return p }

// isLongPathError is Windows-only.
func isLongPathError(error) bool { return false }
