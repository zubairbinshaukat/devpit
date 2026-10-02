//go:build windows

package accounts

import (
	"strings"

	"golang.org/x/sys/windows"
)

// LongPath returns folder with every 8.3 short name (C:\Users\RUNNER~1)
// expanded to its long name (C:\Users\runneradmin), normalized. Junctions
// and subst drives are not followed: that is RealPath's job. A folder that
// does not exist yet is expanded through its nearest existing parent, with
// the rest added back as written. A path that does not parse comes back as
// given.
//
// Tools compare folders by their long names (Git matches `gitdir/i:` rules
// against the real path of the repository), so a folder reached through a
// short name must be written with its long name or the rule never applies.
func LongPath(folder string) string {
	norm, err := NormalizeFolder(folder, "")
	if err != nil {
		return folder
	}
	if !strings.Contains(norm, "~") {
		return norm // only a name with a tilde can be a short name
	}
	vol, parts, err := splitWinPath(norm, "")
	if err != nil {
		return norm
	}
	for n := len(parts); n >= 0; n-- {
		head := joinWinPath(vol, parts[:n])
		long, lerr := longPathName(head)
		if lerr != nil {
			continue
		}
		rest := parts[n:]
		if len(rest) > 0 {
			long = strings.TrimSuffix(long, `\`) + `\` + strings.Join(rest, `\`)
		}
		if out, err := NormalizeFolder(long, ""); err == nil {
			return out
		}
		return norm
	}
	return norm
}

// longPathName asks Windows for the long form of an existing path.
func longPathName(p string) (string, error) {
	name, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, windows.MAX_PATH)
	for {
		n, err := windows.GetLongPathName(name, &buf[0], uint32(len(buf))) //nolint:gosec // len(buf) is bounded below
		if err != nil {
			return "", err
		}
		if int(n) < len(buf) {
			return windows.UTF16ToString(buf[:n]), nil
		}
		if n > 32768 {
			return "", windows.ERROR_INSUFFICIENT_BUFFER
		}
		buf = make([]uint16, n+1)
	}
}
