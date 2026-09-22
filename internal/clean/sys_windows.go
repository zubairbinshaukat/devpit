package clean

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// longPathPrefix is the extended-length prefix that lifts the 260-character
// MAX_PATH limit for the Win32 file APIs.
const longPathPrefix = `\\?\`

// fileAttributes wraps GetFileAttributesW for one path.
func fileAttributes(p string) (uint32, error) {
	wide, err := windows.UTF16PtrFromString(longPath(p))
	if err != nil {
		return 0, err
	}
	return windows.GetFileAttributes(wide)
}

// resolvesThroughReparse reports whether the path is a reparse point or sits
// underneath one. It walks from the path up to the volume root and asks
// GetFileAttributesW about each component, which is the only way to see a
// junction for what it is: opening the path would silently follow it, and that
// is the bug that made other cleaners delete real source trees (safety rule 8).
//
// A component that does not exist is not an error; the scan may have raced
// with Explorer. Any other failure is returned so the pre-flight refuses
// rather than guesses.
func resolvesThroughReparse(p string) (bool, error) {
	cur := filepath.Clean(p)
	for {
		attrs, err := fileAttributes(cur)
		switch {
		case err == nil:
			if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
				return true, nil
			}
		case errors.Is(err, fs.ErrNotExist):
			// Keep climbing; an ancestor may still be a junction.
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

// isNetworkPath reports whether the path lives on a mapped network drive. UNC
// paths are caught by a plain string test before this is reached.
func isNetworkPath(p string) bool {
	vol := filepath.VolumeName(p)
	if len(vol) != 2 || vol[1] != ':' {
		return false
	}
	wide, err := windows.UTF16PtrFromString(vol + `\`)
	if err != nil {
		return false
	}
	return windows.GetDriveType(wide) == windows.DRIVE_REMOTE
}

// isLocked reports whether an operating system error is the kind that means
// "something else has this open". A rename that fails this way is the cheapest
// possible way to discover a folder is locked, before anything is removed.
func isLocked(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch errno {
	case windows.ERROR_SHARING_VIOLATION, windows.ERROR_LOCK_VIOLATION, windows.ERROR_ACCESS_DENIED:
		return true
	default:
		return false
	}
}

// longPath returns the path in extended-length form so calls survive paths
// over MAX_PATH on machines where LongPathsEnabled is 0. Relative paths and
// paths that already carry the prefix are returned untouched.
func longPath(p string) string {
	if strings.HasPrefix(p, longPathPrefix) {
		return p
	}
	if !filepath.IsAbs(p) {
		return p
	}
	if strings.HasPrefix(p, `\\`) {
		return longPathPrefix + `UNC` + p[1:]
	}
	return longPathPrefix + p
}

// isLongPathError reports whether an error looks like it was caused by the
// MAX_PATH limit, in which case the operation is worth retrying with the
// extended-length prefix.
func isLongPathError(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch errno {
	case windows.ERROR_PATH_NOT_FOUND, windows.ERROR_FILENAME_EXCED_RANGE, windows.ERROR_INVALID_NAME:
		return true
	default:
		return false
	}
}
