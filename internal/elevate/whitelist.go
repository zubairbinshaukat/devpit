package elevate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// allowedRemoveRoots lists the only directories the worker will delete
// under. All three are admin-only or awkward enough to need elevation:
// Windows' own temp directory, per-user crash dumps, and Windows Error
// Reporting's store. Everything else is refused, no matter how the request
// was built, because the worker cannot know whether the TUI's own
// never-touch and marker-file checks (internal/scan, internal/clean) ran
// before the request was sent.
func allowedRemoveRoots() []string {
	var roots []string
	if v := os.Getenv("WINDIR"); v != "" {
		roots = append(roots, filepath.Join(v, "Temp"))
	}
	if v := os.Getenv("LOCALAPPDATA"); v != "" {
		roots = append(roots, filepath.Join(v, "CrashDumps"))
	}
	if v := os.Getenv("PROGRAMDATA"); v != "" {
		roots = append(roots, filepath.Join(v, "Microsoft", "Windows", "WER"))
	}
	return roots
}

// isUnderRoot reports whether path is a descendant of root, comparing
// case-insensitively since these are all Windows paths. The root itself does
// not count: deleting %WINDIR%\Temp as a whole is never a cleanup.
func isUnderRoot(path, root string) bool {
	p := filepath.Clean(path)
	r := filepath.Clean(root)
	return strings.HasPrefix(strings.ToLower(p), strings.ToLower(r)+string(filepath.Separator)) &&
		len(p) > len(r)+1
}

// removeRoot returns the allowed cleanup root path is under and path's
// place inside it, or a refusal saying why so the TUI can show a real reason
// instead of "failed".
func removeRoot(path string) (root, rel string, err error) {
	if path == "" {
		return "", "", fmt.Errorf("refused: empty path")
	}
	for _, r := range allowedRemoveRoots() {
		if isUnderRoot(path, r) {
			rel, err := filepath.Rel(filepath.Clean(r), filepath.Clean(path))
			if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
				break
			}
			return filepath.Clean(r), rel, nil
		}
	}
	return "", "", fmt.Errorf("refused: %q is not inside an allowed cleanup root "+
		"(%%WINDIR%%\\Temp, %%LOCALAPPDATA%%\\CrashDumps, %%PROGRAMDATA%%\\Microsoft\\Windows\\WER)", path)
}

// validateRemovePath refuses anything outside [allowedRemoveRoots].
func validateRemovePath(path string) error {
	_, _, err := removeRoot(path)
	return err
}
