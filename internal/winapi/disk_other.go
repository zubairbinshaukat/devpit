//go:build !windows

package winapi

import "errors"

// ErrUnsupported is returned by every stub on non-Windows platforms.
var ErrUnsupported = errors.New("winapi: not supported on this platform")

// SystemDrive returns the filesystem root on non-Windows platforms.
func SystemDrive() string { return "/" }

// FreeSpace is not implemented outside Windows. Milestone 8 may add a
// statfs-based implementation when Linux support lands.
func FreeSpace(string) (DiskSpace, error) { return DiskSpace{}, ErrUnsupported }
