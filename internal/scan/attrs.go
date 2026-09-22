package scan

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// isLeafMode reports whether a directory entry's mode marks it as something
// the walker must treat as a leaf: a symlink, a junction or any other reparse
// point. Go reports a junction as ModeIrregular and a symlink as ModeSymlink,
// and sets neither ModeDir, so this test is enough on its own; the attribute
// check in isReparseInfo is the second, independent guard.
func isLeafMode(mode fs.FileMode) bool {
	return mode&(fs.ModeSymlink|fs.ModeIrregular) != 0
}

// IsReparsePoint reports whether path itself is a reparse point: a junction,
// a symbolic link, a mount point or an AppExecLink. It does not follow the
// path, and it does not look at the path's ancestors.
func IsReparsePoint(path string) (bool, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	if isLeafMode(fi.Mode()) {
		return true, nil
	}
	return isReparseInfo(fi), nil
}

// ResolvesThroughReparse reports whether path, or any directory above it, is
// a reparse point. The delete pre-flight uses it: renaming inside a junction
// would move the link's target, not a copy of it.
//
// Components that do not exist are not an error: a path whose leaf has
// already been removed still answers the question about its ancestors.
func ResolvesThroughReparse(path string) (bool, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	abs = filepath.Clean(abs)

	var firstErr error
	for cur := abs; ; {
		is, err := IsReparsePoint(cur)
		switch {
		case err == nil && is:
			return true, nil
		case err != nil && !os.IsNotExist(err) && firstErr == nil:
			firstErr = err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return false, firstErr
}

// isCloudInfo reports whether a directory entry is a cloud placeholder whose
// contents live on a server. Reading one downloads it, so the walker skips it
// and counts zero local bytes.
func isCloudInfo(fi fs.FileInfo) bool { return isCloudAttr(fi) }

// entrySize returns an entry's size in bytes without a second stat call: the
// FileInfo comes from the directory listing the walker already read.
func entrySize(fi fs.FileInfo) uint64 {
	if n := fi.Size(); n > 0 {
		return uint64(n)
	}
	return 0
}

// pathIsUNC reports whether a path is a UNC path such as \\server\share.
// Devpit refuses network roots by default: a scan over SMB is slow enough to
// look like a hang, and a delete over SMB has no Recycle Bin.
func pathIsUNC(path string) bool {
	if len(path) < 2 {
		return false
	}
	return (path[0] == '\\' || path[0] == '/') && (path[1] == '\\' || path[1] == '/')
}

// underPath reports whether child is path itself or something beneath it.
func underPath(child, parent string) bool {
	c, p := normalizePath(child), normalizePath(parent)
	if strings.EqualFold(c, p) {
		return true
	}
	if !strings.HasSuffix(p, string(filepath.Separator)) {
		p += string(filepath.Separator)
	}
	return strings.HasPrefix(strings.ToLower(c), strings.ToLower(p))
}

// normalizePath cleans a path and gives it the platform's separator, without
// touching the filesystem.
func normalizePath(p string) string {
	if p == "" {
		return p
	}
	return filepath.Clean(filepath.FromSlash(p))
}

// isDriveRoot reports whether path is the root of a volume, such as "C:\" or
// "/". Devpit never deletes one.
func isDriveRoot(path string) bool {
	clean := normalizePath(path)
	if clean == "" {
		return false
	}
	vol := filepath.VolumeName(clean)
	rest := strings.TrimPrefix(clean, vol)
	rest = strings.Trim(rest, `\/`)
	return rest == ""
}
