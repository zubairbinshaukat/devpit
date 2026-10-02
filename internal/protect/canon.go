package protect

import (
	"path/filepath"
	"strings"
)

// canon turns a path into the spelling every comparison is made on: device
// prefix removed, separators made the platform's, absolute, cleaned, and on
// Windows with trailing dots and spaces dropped from each component and 8.3
// short names expanded. It reports false for an empty or blank path, which
// must never match anything.
//
// It never fails closed into "everything": a path that cannot be made
// absolute is reported as unusable rather than guessed at.
func canon(p string) (string, bool) {
	if strings.TrimSpace(p) == "" {
		return "", false
	}
	p = filepath.FromSlash(stripDevicePrefix(p))
	if !filepath.IsAbs(p) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", false
		}
		p = abs
	}
	p = platformCanon(filepath.Clean(p))
	if p == "" {
		return "", false
	}
	return p, true
}

// stripDevicePrefix removes the Win32 device and NT namespace prefixes that
// name an ordinary path a second way: `\\?\C:\x` and `\\.\C:\x` are `C:\x`,
// and `\\?\UNC\server\share` is `\\server\share`. Either slash works. A
// prefix in front of anything other than a drive letter or UNC (a volume
// GUID, a pipe) is left alone, because it is not a second name for a folder.
func stripDevicePrefix(p string) string {
	if len(p) < 4 {
		return p
	}
	norm := strings.ReplaceAll(p[:4], "/", `\`)
	switch norm {
	case `\\?\`, `\\.\`, `\??\`:
	default:
		return p
	}
	rest := p[4:]
	if len(rest) >= 4 && strings.EqualFold(strings.ReplaceAll(rest[:4], "/", `\`), `UNC\`) {
		return `\\` + rest[4:]
	}
	if len(rest) >= 2 && rest[1] == ':' && isLetter(rest[0]) {
		return rest
	}
	return p
}

// isLetter reports whether b is an ASCII letter, which is what a drive
// letter is.
func isLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
