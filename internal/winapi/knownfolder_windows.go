//go:build windows

package winapi

import "golang.org/x/sys/windows"

func downloadsFolder() (string, error) {
	return windows.KnownFolderPath(windows.FOLDERID_Downloads, 0)
}
