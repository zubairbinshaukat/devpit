package accounts

import (
	"errors"
	"fmt"
	"strings"
)

// Folder paths in rules are Windows paths, and they are compared the way
// Windows compares them, on every OS: the tests for this run on the Linux CI
// runner too, so nothing here uses path/filepath.

// ErrRelativePath is returned for a relative folder when no base folder was
// given to resolve it against.
var ErrRelativePath = errors.New("the folder is relative and there is no current folder to resolve it against")

// NormalizeFolder returns the canonical spelling of a Windows folder path:
//
//   - surrounding spaces and quotes removed ("C:\Work" pasted with quotes);
//   - forward slashes turned into backslashes;
//   - the \\?\ and \\?\UNC\ prefixes and the \??\ NT prefix removed;
//   - the drive letter in upper case;
//   - "." and ".." resolved, never climbing above the drive or share;
//   - trailing dots and spaces dropped from each name, as Windows does;
//   - no trailing backslash, except on a drive root ("C:\").
//
// A relative path ("src", "..\x", "\Work" or "C:Work") is resolved against
// base, which must itself be absolute. Case is kept: comparisons use
// [SameFolder] and [FolderContains], which ignore it.
func NormalizeFolder(p, base string) (string, error) {
	vol, parts, err := splitWinPath(p, base)
	if err != nil {
		return "", err
	}
	return joinWinPath(vol, parts), nil
}

// splitWinPath parses p into its volume ("C:" or `\\server\share`) and its
// cleaned components.
func splitWinPath(p, base string) (string, []string, error) {
	s := strings.TrimSpace(p)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	if s == "" {
		return "", nil, errors.New("the folder is empty")
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return "", nil, fmt.Errorf("the folder %q contains a control character", p)
		}
		switch r {
		case '<', '>', '|', '*', '?', '"':
			// '?' is allowed only as part of the \\?\ prefix, handled below.
			if r == '?' && (strings.HasPrefix(s, `\\?\`) || strings.HasPrefix(s, `//?/`) || strings.HasPrefix(s, `\??\`)) {
				continue
			}
			return "", nil, fmt.Errorf("the folder %q contains %q, which Windows does not allow in a path", p, r)
		}
	}
	s = strings.ReplaceAll(s, "/", `\`)

	switch {
	case hasFoldPrefix(s, `\\?\UNC\`):
		s = `\\` + s[len(`\\?\UNC\`):]
	case strings.HasPrefix(s, `\\?\`), strings.HasPrefix(s, `\??\`):
		s = s[4:]
	case strings.HasPrefix(s, `\\.\`):
		return "", nil, fmt.Errorf("%q is a device path, not a folder", p)
	}
	if strings.ContainsRune(s, '?') {
		return "", nil, fmt.Errorf("the folder %q contains '?', which Windows does not allow in a path", p)
	}
	if i := strings.LastIndexByte(s, ':'); i >= 0 && i != 1 {
		return "", nil, fmt.Errorf("the folder %q has a ':' that is not part of a drive letter", p)
	}

	// Drive letter, absolute or drive-relative.
	if len(s) >= 2 && s[1] == ':' && isLetter(s[0]) {
		vol := strings.ToUpper(s[:2])
		rest := s[2:]
		if strings.HasPrefix(rest, `\`) {
			return vol, cleanParts(nil, rest), nil
		}
		// "C:Work": relative to the current folder on C:, which is the base
		// when the base is on C: and the drive root otherwise.
		if base != "" {
			bv, bp, err := splitWinPath(base, "")
			if err == nil && strings.EqualFold(bv, vol) {
				return vol, cleanParts(bp, rest), nil
			}
		}
		return vol, cleanParts(nil, rest), nil
	}

	// UNC: \\server\share\...
	if strings.HasPrefix(s, `\\`) {
		segs := strings.Split(s[2:], `\`)
		if len(segs) < 2 || segs[0] == "" || segs[1] == "" {
			return "", nil, fmt.Errorf("%q is not a complete network path; it needs \\\\server\\share", p)
		}
		vol := `\\` + segs[0] + `\` + segs[1]
		return vol, cleanParts(nil, strings.Join(segs[2:], `\`)), nil
	}

	// Relative to the base: "\Work" keeps only the base's volume.
	if base == "" {
		return "", nil, fmt.Errorf("%q: %w", p, ErrRelativePath)
	}
	bv, bp, err := splitWinPath(base, "")
	if err != nil {
		return "", nil, fmt.Errorf("the current folder %q: %w", base, err)
	}
	if strings.HasPrefix(s, `\`) {
		return bv, cleanParts(nil, s), nil
	}
	return bv, cleanParts(bp, s), nil
}

// cleanParts appends the components of rest to parts, resolving "." and "..".
func cleanParts(parts []string, rest string) []string {
	out := append([]string(nil), parts...)
	for _, seg := range strings.Split(rest, `\`) {
		switch seg {
		case "", ".":
			continue
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
			continue
		}
		seg = strings.TrimRight(seg, ". ")
		if seg == "" {
			continue
		}
		out = append(out, seg)
	}
	return out
}

// joinWinPath is the inverse of splitWinPath.
func joinWinPath(vol string, parts []string) string {
	if len(parts) == 0 {
		if strings.HasPrefix(vol, `\\`) {
			return vol
		}
		return vol + `\`
	}
	return vol + `\` + strings.Join(parts, `\`)
}

// folderKey is the form two folders are compared in: normalized, then upper
// case, as NTFS compares names. It is "" for a path that does not parse.
func folderKey(p string) string {
	n, err := NormalizeFolder(p, "")
	if err != nil {
		return ""
	}
	return strings.ToUpper(n)
}

// SameFolder reports whether a and b name the same folder, the way Windows
// compares paths: ignoring case, slashes, trailing separators and prefixes.
func SameFolder(a, b string) bool {
	ka, kb := folderKey(a), folderKey(b)
	return ka != "" && ka == kb
}

// FolderContains reports whether child is parent or inside it. C:\Work
// contains C:\Work\api but not C:\Workshop; C:\ contains everything on C:.
func FolderContains(parent, child string) bool {
	return keyContains(folderKey(parent), folderKey(child))
}

// keyContains is FolderContains on keys that are already normalized.
func keyContains(pk, ck string) bool {
	if pk == "" || ck == "" {
		return false
	}
	if pk == ck {
		return true
	}
	return strings.HasPrefix(ck, strings.TrimSuffix(pk, `\`)+`\`)
}

// folderDepth counts the components below the volume: 0 for C:\.
func folderDepth(p string) int {
	_, parts, err := splitWinPath(p, "")
	if err != nil {
		return -1
	}
	return len(parts)
}

// FolderVolume returns the volume of a normalized folder: "C:\" or
// `\\server\share`.
func FolderVolume(p string) string {
	vol, _, err := splitWinPath(p, "")
	if err != nil {
		return ""
	}
	return joinWinPath(vol, nil)
}

// IsDriveRoot reports whether p is the root of a drive or a share.
func IsDriveRoot(p string) bool {
	return folderDepth(p) == 0
}

// IsNetworkPath reports whether p is a UNC path.
func IsNetworkPath(p string) bool {
	vol, _, err := splitWinPath(p, "")
	return err == nil && strings.HasPrefix(vol, `\\`)
}

func isLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func hasFoldPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}
