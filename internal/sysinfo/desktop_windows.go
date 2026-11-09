//go:build windows

package sysinfo

import "golang.org/x/sys/windows"

// DesktopDir is the user's Desktop folder (which OneDrive may have moved).
func DesktopDir() (string, error) {
	return windows.KnownFolderPath(windows.FOLDERID_Desktop, 0)
}
