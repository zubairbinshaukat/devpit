package clean

import (
	"os"
	"path/filepath"
	"strings"
)

// normalize turns a user- or scanner-supplied path into the absolute, cleaned
// form the rest of the package compares against. It never touches the disk.
func normalize(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", ErrEmptyPath
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

// sameDir reports whether two cleaned paths are the same path, ignoring case
// the way Windows does.
func samePath(a, b string) bool { return strings.EqualFold(a, b) }

// within reports whether child is parent or sits underneath it. Both arguments
// must already be cleaned absolute paths. The comparison is case-insensitive
// and component-aware, so C:\foobar is not inside C:\foo.
func within(child, parent string) bool {
	if parent == "" || child == "" {
		return false
	}
	if samePath(child, parent) {
		return true
	}
	// A root such as "C:\" or "/" already ends in a separator; anything else
	// needs one added before the prefix test so a shared name prefix does not
	// count as containment.
	prefix := parent
	if !strings.HasSuffix(prefix, string(os.PathSeparator)) {
		prefix += string(os.PathSeparator)
	}
	if len(child) <= len(prefix) {
		return false
	}
	return strings.EqualFold(child[:len(prefix)], prefix)
}

// isDriveRoot reports whether a cleaned absolute path is the root of a volume
// or of a UNC share: "C:\", "\\server\share", "/".
func isDriveRoot(p string) bool {
	vol := filepath.VolumeName(p)
	if vol != "" {
		rest := p[len(vol):]
		return rest == "" || rest == string(os.PathSeparator) || rest == "/"
	}
	return p == string(os.PathSeparator) || p == "/"
}

// isUNC reports whether a path names a UNC share. This is a string test so it
// behaves the same on every platform and in tests.
func isUNC(p string) bool {
	vol := filepath.VolumeName(p)
	return strings.HasPrefix(vol, `\\`) || strings.HasPrefix(vol, "//") ||
		strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, "//")
}

// windowsDir returns the cleaned Windows directory, or "" when the environment
// does not name one (which is every non-Windows machine).
func windowsDir() string {
	for _, env := range []string{"windir", "SystemRoot"} {
		if v := os.Getenv(env); v != "" {
			if abs, err := normalize(v); err == nil {
				return abs
			}
		}
	}
	return ""
}

// ownExecutable returns the cleaned path of the running binary, or "" when the
// operating system will not say. It resolves symlinks so a launcher shim
// cannot be used to slip past the check.
func ownExecutable() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, evalErr := filepath.EvalSymlinks(exe); evalErr == nil {
		exe = resolved
	}
	abs, absErr := normalize(exe)
	if absErr != nil {
		return ""
	}
	return abs
}

// resolvedPath returns p with symlinks and Windows 8.3 short names expanded,
// or p unchanged when the path cannot be resolved — it may not exist, and a
// refusal check must still work on a path that is already gone.
//
// This matters because one directory has more than one spelling.
// C:\Users\RUNNER~1\AppData\Local\Temp and
// C:\Users\runneradmin\AppData\Local\Temp are the same directory, and a
// never-touch check that compared only the spelling it was handed would let
// the other one through.
func resolvedPath(p string) string {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p
	}
	abs, err := normalize(r)
	if err != nil {
		return p
	}
	return abs
}

// pathSpellings returns the distinct spellings of one cleaned absolute path
// that a containment check has to consider: the path as given, and its
// resolved form when that differs.
func pathSpellings(p string) []string {
	if r := resolvedPath(p); !samePath(r, p) {
		return []string{p, r}
	}
	return []string{p}
}
