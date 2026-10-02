package protect

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// platformCanon applies the two Win32 spellings a plain clean misses.
//
// Win32 drops trailing dots and spaces from every component, so
// `C:\Users\me\.ssh.` opens `C:\Users\me\.ssh`; the comparison drops them too.
// A short name such as `C:\Users\RUNNER~1` is expanded to its long form when
// the path, or the part of it that exists, can be looked up. Only a path that
// holds a `~` pays for that lookup, which keeps the scanner's per-directory
// check a string comparison.
func platformCanon(p string) string {
	p = trimComponents(p)
	if strings.Contains(p, "~") {
		p = expandShort(p)
	}
	return p
}

// trimComponents drops trailing dots and spaces from each component after the
// volume name. A component that is nothing but dots or spaces is kept as it
// is: emptying it would change which folder the path names.
func trimComponents(p string) string {
	vol := filepath.VolumeName(p)
	rest := p[len(vol):]
	parts := strings.Split(rest, `\`)
	changed := false
	for i, part := range parts {
		t := strings.TrimRight(part, ". ")
		if t != part && t != "" {
			parts[i] = t
			changed = true
		}
	}
	if !changed {
		return p
	}
	return vol + strings.Join(parts, `\`)
}

// expandShort expands 8.3 short names in p. When p itself does not exist, the
// longest existing parent is expanded and the rest is put back, so a
// protected folder that has not been created yet is still matched under a
// short-named profile.
func expandShort(p string) string {
	cur, tail := p, ""
	for {
		if long, ok := longPathName(cur); ok {
			if tail == "" {
				return long
			}
			return filepath.Join(long, tail)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		if tail == "" {
			tail = filepath.Base(cur)
		} else {
			tail = filepath.Join(filepath.Base(cur), tail)
		}
		cur = parent
	}
}

// longPathName is GetLongPathNameW: the long form of an existing path.
func longPathName(p string) (string, bool) {
	in, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return "", false
	}
	size := uint32(windows.MAX_PATH)
	for {
		buf := make([]uint16, size)
		n, err := windows.GetLongPathName(in, &buf[0], size)
		if err != nil || n == 0 {
			return "", false
		}
		if n < size {
			return windows.UTF16ToString(buf[:n]), true
		}
		size = n + 1
	}
}

// volumeNameDOS asks GetFinalPathNameByHandleW for a drive-letter path
// (VOLUME_NAME_DOS, which x/sys/windows does not name).
const volumeNameDOS = 0x0

// finalPath resolves an existing path through every junction, symlink and
// short name to the one the filesystem actually holds, with
// GetFinalPathNameByHandleW. It reports false when the path does not exist
// or cannot be opened, which for a protected folder that has not been made
// yet is the ordinary case.
func finalPath(p string) (string, bool) {
	in, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return "", false
	}
	// No access rights are asked for: the handle is only used to read its
	// own name, and FILE_FLAG_BACKUP_SEMANTICS is what lets it open a folder.
	h, err := windows.CreateFile(in, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", false
	}
	defer func() { _ = windows.CloseHandle(h) }()

	size := uint32(windows.MAX_PATH)
	for {
		buf := make([]uint16, size)
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], size, volumeNameDOS)
		if err != nil || n == 0 {
			return "", false
		}
		if n < size {
			return stripDevicePrefix(windows.UTF16ToString(buf[:n])), true
		}
		size = n + 1
	}
}
