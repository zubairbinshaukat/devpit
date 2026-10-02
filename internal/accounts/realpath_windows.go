//go:build windows

package accounts

import (
	"strings"

	"golang.org/x/sys/windows"
)

// RealPath returns where folder really is after junctions, symbolic links,
// subst drives, mapped network drives and 8.3 short names. A folder that
// does not exist is resolved through its nearest existing parent, with the
// rest of the path added back, so a rule can be checked for a folder that
// is about to be created.
func RealPath(folder string) (string, error) {
	norm, err := NormalizeFolder(folder, "")
	if err != nil {
		return "", err
	}
	vol, parts, err := splitWinPath(norm, "")
	if err != nil {
		return "", err
	}
	for n := len(parts); n >= 0; n-- {
		head := joinWinPath(vol, parts[:n])
		final, ferr := finalPath(head)
		if ferr != nil {
			continue
		}
		rest := parts[n:]
		if len(rest) == 0 {
			return NormalizeFolder(final, "")
		}
		return NormalizeFolder(final+`\`+strings.Join(rest, `\`), "")
	}
	return norm, nil
}

// finalPath asks Windows where an existing path ends up. FILE_FLAG_BACKUP_
// SEMANTICS opens a folder without any access rights.
func finalPath(p string) (string, error) {
	name, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return "", err
	}
	h, err := windows.CreateFile(name, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h) //nolint:errcheck // a query-only handle

	buf := make([]uint16, windows.MAX_PATH)
	for {
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0) //nolint:gosec // len(buf) is bounded below
		if err != nil {
			return "", err
		}
		if int(n) < len(buf) {
			out := windows.UTF16ToString(buf[:n])
			if rest, ok := strings.CutPrefix(out, `\\?\UNC\`); ok {
				return `\\` + rest, nil
			}
			return strings.TrimPrefix(out, `\\?\`), nil
		}
		if n > 32768 {
			return "", windows.ERROR_INSUFFICIENT_BUFFER
		}
		buf = make([]uint16, n+1)
	}
}
