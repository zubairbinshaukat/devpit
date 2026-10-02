//go:build windows

package importer

import "golang.org/x/sys/windows"

// documentsFolder is the real Documents folder (it may be redirected to
// OneDrive or another drive), or "".
func documentsFolder() string {
	p, err := windows.KnownFolderPath(windows.FOLDERID_Documents, 0)
	if err != nil {
		return ""
	}
	return p
}
