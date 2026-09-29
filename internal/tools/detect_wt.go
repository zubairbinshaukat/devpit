package tools

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// windowsAppsSegment is the folder every Microsoft Store alias lives in
// (%LOCALAPPDATA%\Microsoft\WindowsApps), lowercased with backslashes so
// [isStoreStub] can match a path spelled either way.
const windowsAppsSegment = `\microsoft\windowsapps\`

// wtPackagePattern reads the version out of Windows Terminal's package folder
// name, "Microsoft.WindowsTerminal_1.21.3231.0_x64__8wekyb3d8bbwe". The
// trailing underscore keeps the Preview package (whose name continues with
// "Preview") from matching.
var wtPackagePattern = regexp.MustCompile(`^Microsoft\.WindowsTerminal_(\d+(?:\.\d+){1,3})_`)

// detectPassive finds a tool that must not be started (see
// [toolSpec.neverRun]) by looking at the file system and the environment
// only. Nothing here executes anything.
//
// Windows Terminal is looked for in this order, cheapest first:
//
//  1. PATH, where the Store's "wt.exe" alias sits for a normal install;
//  2. the alias's own folder, %LOCALAPPDATA%\Microsoft\WindowsApps, for a
//     PATH that lost it;
//  3. WT_SESSION, which Windows Terminal sets in every tab it hosts, so a
//     Devpit running inside it knows for certain, even when the alias is
//     missing.
//
// The version is read from the package folder's name when that folder can be
// listed (WindowsApps is often unreadable for a standard user, and then the
// version is simply left empty). It is never read by running wt.
func (d *Detector) detectPassive(spec toolSpec) Tool {
	tool := Tool{Name: spec.name, Exe: spec.exe}

	if path, err := d.lookPath(spec.exe); err == nil {
		tool.Path, tool.Found = path, true
	} else if local := d.getenv("LOCALAPPDATA"); local != "" {
		alias := filepath.Join(local, "Microsoft", "WindowsApps", spec.exe+".exe")
		if d.stat(alias) == nil {
			tool.Path, tool.Found = alias, true
		}
	}
	if !tool.Found && spec.name == "wt" && d.getenv("WT_SESSION") != "" {
		tool.Found = true
	}
	if tool.Found && spec.name == "wt" {
		tool.Version = d.windowsTerminalVersion()
	}
	return tool
}

// windowsTerminalVersion is the highest version among the Windows Terminal
// package folders under %ProgramFiles%\WindowsApps, or "" when the folder
// cannot be listed or holds none.
func (d *Detector) windowsTerminalVersion() string {
	pf := d.getenv("ProgramFiles")
	if pf == "" {
		return ""
	}
	names, err := d.readDir(filepath.Join(pf, "WindowsApps"))
	if err != nil {
		return ""
	}
	best := ""
	for _, n := range names {
		if m := wtPackagePattern.FindStringSubmatch(n); m != nil && (best == "" || versionLess(best, m[1])) {
			best = m[1]
		}
	}
	return best
}

// versionLess reports whether dotted version a is older than b, comparing
// each part as a number so 1.9 is older than 1.10.
func versionLess(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := range max(len(as), len(bs)) {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}

// isStoreStub reports whether path is inside the WindowsApps alias folder.
func isStoreStub(path string) bool {
	p := strings.ToLower(strings.ReplaceAll(path, "/", `\`))
	return strings.Contains(p, windowsAppsSegment)
}

// statFile is the default [WithStat]: nil when path exists.
func statFile(path string) error {
	_, err := os.Stat(path)
	return err
}

// readDirNames is the default [WithReadDir]: the names in dir.
func readDirNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return names, nil
}
