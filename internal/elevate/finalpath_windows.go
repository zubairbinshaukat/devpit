//go:build windows

package elevate

import (
	"strings"

	"golang.org/x/sys/windows"
)

// volumeNameDOS is VOLUME_NAME_DOS|FILE_NAME_NORMALIZED (both zero): a
// drive-letter path with every component's long, on-disk name.
const volumeNameDOS = 0

// finalPath returns the path Windows itself ends up at for p, after every
// junction, symbolic link and 8.3 short name on the way. filepath.EvalSymlinks
// is not enough: since Go 1.23 it no longer treats a junction as a link, so
// a junction to C:\Windows would come back unchanged.
func finalPath(p string) (string, error) {
	name, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return "", err
	}
	// No access rights are needed just to ask for the name, and
	// FILE_FLAG_BACKUP_SEMANTICS is what lets CreateFile open a folder.
	h, err := windows.CreateFile(name, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h) //nolint:errcheck // a query-only handle

	var buf [windows.MAX_LONG_PATH]uint16
	n, err := windows.GetFinalPathNameByHandle(h, &buf[0], windows.MAX_LONG_PATH, volumeNameDOS)
	if err != nil {
		return "", err
	}
	if int(n) > len(buf) {
		return "", windows.ERROR_INSUFFICIENT_BUFFER
	}
	out := windows.UTF16ToString(buf[:n])
	if rest, ok := strings.CutPrefix(out, `\\?\UNC\`); ok {
		return `\\` + rest, nil
	}
	return strings.TrimPrefix(out, `\\?\`), nil
}
