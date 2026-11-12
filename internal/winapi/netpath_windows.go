//go:build windows

package winapi

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// isRemoteDrive is the GetDriveType == DRIVE_REMOTE test, on the drive letter
// of p (after a \\?\ prefix, if any).
func isRemoteDrive(p string) bool {
	p = strings.TrimPrefix(p, `\\?\`)
	vol := filepath.VolumeName(p)
	if len(vol) != 2 || vol[1] != ':' {
		return false
	}
	root, err := windows.UTF16PtrFromString(vol + `\`)
	if err != nil {
		return false
	}
	return windows.GetDriveType(root) == windows.DRIVE_REMOTE
}
