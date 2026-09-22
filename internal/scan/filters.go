package scan

import (
	"os"
	"path/filepath"
	"strings"
)

// alwaysSkipNames are directory names that are never worth descending into,
// wherever they turn up. ".git" is the important one: it is never junk, it is
// full of small files, and walking it is pure cost.
var alwaysSkipNames = map[string]struct{}{
	".git":                      {},
	"$recycle.bin":              {},
	"system volume information": {},
}

// systemRootNames are skipped when they sit directly under a drive root. The
// test is deliberately positional: a folder called "windows" inside a project
// is somebody's source code, while C:\Windows is the operating system.
func isSystemRootName(lower string) bool {
	switch {
	case lower == "windows", lower == "winnt":
		return true
	case strings.HasPrefix(lower, "program files"):
		return true
	case lower == "$windows.~bt", lower == "$windows.~ws", lower == "recovery":
		return true
	default:
		return false
	}
}

// filters decide which directories a scan refuses to enter. Every field is
// read-only once built, so the walk callback can consult it from every worker
// goroutine without locking.
type filters struct {
	// neverTouch is the user's list, normalised to absolute paths.
	neverTouch []string
	// system is the set of locations Devpit never scans: the Windows
	// directory and the Program Files trees.
	system []string
	// oneDrive is the set of cloud-sync roots, skipped unless the user opted
	// in, because reading a placeholder downloads it.
	oneDrive []string
	// includeOneDrive turns the oneDrive list off.
	includeOneDrive bool
}

// newFilters builds the filter set for one scan.
func newFilters(opts Options) filters {
	f := filters{includeOneDrive: opts.IncludeOneDrive}

	for _, p := range opts.NeverTouch {
		if strings.TrimSpace(p) == "" {
			continue
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		f.neverTouch = append(f.neverTouch, normalizePath(abs))
	}

	for _, env := range []string{"WINDIR", "SystemRoot", "ProgramFiles", "ProgramFiles(x86)", "ProgramW6432"} {
		if v := os.Getenv(env); v != "" {
			f.system = append(f.system, normalizePath(v))
		}
	}

	if !opts.IncludeOneDrive {
		f.oneDrive = opts.oneDriveRoots()
	}

	return f
}

// oneDriveRoots resolves the cloud-sync roots the OneDrive skip applies to.
// It first tries the OneDrive/OneDriveConsumer/OneDriveCommercial
// environment variables OneDrive normally sets. Some installs (corporate
// images, scripted setups, a OneDrive that started after these were read)
// leave those unset even though a sync root exists on disk, so as a fallback
// this also globs %USERPROFILE%\OneDrive* for directories — every OneDrive
// build, personal or work-or-school, creates its root there regardless of
// environment state. The result is deduplicated and normalized.
func (o Options) oneDriveRoots() []string {
	seen := make(map[string]struct{})
	var out []string
	add := func(p string) {
		if p == "" {
			return
		}
		norm := normalizePath(p)
		if _, dup := seen[norm]; dup {
			return
		}
		seen[norm] = struct{}{}
		out = append(out, norm)
	}

	for _, env := range []string{"OneDrive", "OneDriveConsumer", "OneDriveCommercial"} {
		add(os.Getenv(env))
	}

	if profile := os.Getenv("USERPROFILE"); profile != "" {
		matches, err := filepath.Glob(filepath.Join(profile, "OneDrive*"))
		if err == nil {
			for _, m := range matches {
				if fi, statErr := os.Stat(m); statErr == nil && fi.IsDir() { //nolint:gosec // m comes from filepath.Glob of %USERPROFILE%\OneDrive*, not user input.
					add(m)
				}
			}
		}
	}

	return out
}

// skipDir reports whether a directory should be skipped outright, given its
// full path and its lower-cased base name.
func (f filters) skipDir(path, lowerName string) bool {
	if _, bad := alwaysSkipNames[lowerName]; bad {
		return true
	}
	if isSystemRootName(lowerName) && isDriveRoot(filepath.Dir(path)) {
		return true
	}
	return f.skipPath(path)
}

// skipPath reports whether a path is inside somewhere the scan must not go:
// the never-touch list, a system tree, or a cloud-sync folder.
//
// Rule 6 is enforced here and, independently, again in the delete pre-flight.
// Two checks on two sides of the program is the point: neither is allowed to
// be the only one.
func (f filters) skipPath(path string) bool {
	for _, p := range f.neverTouch {
		if underPath(path, p) {
			return true
		}
	}
	for _, p := range f.system {
		if underPath(path, p) {
			return true
		}
	}
	if !f.includeOneDrive {
		for _, p := range f.oneDrive {
			if underPath(path, p) {
				return true
			}
		}
	}
	return false
}
