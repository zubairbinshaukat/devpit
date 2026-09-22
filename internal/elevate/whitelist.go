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

// isUnderRoot reports whether path is root itself or a descendant of it,
// comparing case-insensitively since these are all Windows paths.
func isUnderRoot(path, root string) bool {
	p := filepath.Clean(path)
	r := filepath.Clean(root)
	if strings.EqualFold(p, r) {
		return true
	}
	return strings.HasPrefix(strings.ToLower(p), strings.ToLower(r)+string(filepath.Separator))
}

// validateRemovePath refuses anything outside [allowedRemoveRoots], saying
// why so the TUI can show a real reason instead of "failed".
func validateRemovePath(path string) error {
	if path == "" {
		return fmt.Errorf("refused: empty path")
	}
	for _, root := range allowedRemoveRoots() {
		if isUnderRoot(path, root) {
			return nil
		}
	}
	return fmt.Errorf("refused: %q is not under an allowed cleanup root "+
		"(%%WINDIR%%\\Temp, %%LOCALAPPDATA%%\\CrashDumps, %%PROGRAMDATA%%\\Microsoft\\Windows\\WER)", path)
}
