package extract

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/tools"
)

var versionPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

// UseTool turns a detection result into a Tool, or one of the two errors.
// The path must be the detector's resolved absolute path (never a bare name,
// so nothing is looked up in the current folder) and the version at least
// [MinVersion]; newer is fine (decision 14). Rule 83.
//
// The detector resolves Scoop's shim (…\scoop\shims\hatchzip.exe). Spike X8
// checked it is fine to run as is: Ctrl+Break reaches hatchzip through it,
// exit codes pass through, and killing the shim kills hatchzip.
func UseTool(t tools.Tool) (Tool, error) {
	if !t.Found || t.Path == "" || !filepath.IsAbs(t.Path) {
		return Tool{}, ErrToolMissing
	}
	if !versionAtLeast(t.Version, MinVersion) {
		return Tool{}, ErrToolTooOld
	}
	return Tool{Path: t.Path, Version: t.Version}, nil
}

// versionAtLeast compares the leading x.y.z of v with least numerically. An
// unparseable v is never at least anything.
func versionAtLeast(v, least string) bool {
	a := versionPattern.FindStringSubmatch(strings.TrimSpace(v))
	b := versionPattern.FindStringSubmatch(strings.TrimSpace(least))
	if a == nil || b == nil {
		return false
	}
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(a[i])
		y, _ := strconv.Atoi(b[i])
		if x != y {
			return x > y
		}
	}
	return true
}

// bug79FixedIn is the first hatchzip release with the fix for its false
// "disk full" on NTFS in place (issue #79, PR #100). Empty means no release
// has it yet, so the automatic resume (section 6.7) is on for every
// version. Set it when a release ships the fix.
const bug79FixedIn = ""

// bug79Applies reports whether hatchzip version still has bug #79 when the
// fix shipped in fixedIn ("" = not shipped). An unparseable version is
// treated as affected: the auto-resume is safe either way.
func bug79Applies(version, fixedIn string) bool {
	if fixedIn == "" {
		return true
	}
	if versionPattern.FindStringSubmatch(strings.TrimSpace(version)) == nil {
		return true
	}
	return !versionAtLeast(version, fixedIn)
}
