package tools

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
)

// windowsAppsSegment is the folder every Microsoft Store alias lives in
// (%LOCALAPPDATA%\Microsoft\WindowsApps), lowercased with backslashes so
// [isStoreStub] can match a path spelled either way.
const windowsAppsSegment = `\microsoft\windowsapps\`

// wtPackagePattern reads the version out of Windows Terminal's package folder
// name, "Microsoft.WindowsTerminal_1.21.3231.0_x64__8wekyb3d8bbwe". Only the
// main package counts, the one built for a processor: the underscore after
// the name keeps the Preview package ("Microsoft.WindowsTerminalPreview_…")
// out, and the architecture keeps out the "neutral" resource and bundle
// packages beside it, whose version does not have to match. A real Windows 11
// install has "Microsoft.WindowsTerminal_3001.24.11911.0_neutral_~_…" next to
// "Microsoft.WindowsTerminal_1.24.11911.0_x64__…".
var wtPackagePattern = regexp.MustCompile(`(?i)^Microsoft\.WindowsTerminal_(\d+(?:\.\d+){1,3})_(?:x64|x86|arm64|arm)_`)

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
// The version is never read by running wt. It comes from, in order:
//
//  1. the alias itself: the Store's wt.exe is an "app execution alias", a
//     reparse point whose data names the exe it stands for, inside the
//     package folder whose name holds the version. Reading that data opens
//     the alias as a file; it starts nothing;
//  2. the package folder names under %ProgramFiles%\WindowsApps, when that
//     folder can be listed (it often cannot for a standard user).
//
// When neither works the version is left empty.
func (d *Detector) detectPassive(spec toolSpec) Tool {
	tool := Tool{Name: spec.name, Exe: spec.exe}

	alias := ""
	if local := d.getenv("LOCALAPPDATA"); local != "" {
		alias = filepath.Join(local, "Microsoft", "WindowsApps", spec.exe+".exe")
	}
	if path, err := d.lookPath(spec.exe); err == nil {
		tool.Path, tool.Found = path, true
		if isStoreStub(path) {
			alias = path
		}
	} else if alias != "" && d.stat(alias) == nil {
		tool.Path, tool.Found = alias, true
	}
	if !tool.Found && spec.name == "wt" && d.getenv("WT_SESSION") != "" {
		tool.Found = true
	}
	if tool.Found && spec.name == "wt" {
		if tool.Path != "" && !isStoreStub(tool.Path) {
			// A wt.exe that is not the Store's alias (a package folder put
			// on PATH, a Scoop or portable copy): only its own path can say
			// its version. The Store's packages would describe another copy.
			tool.Version = wtVersionFromPath(tool.Path)
		} else {
			tool.Version = d.windowsTerminalVersion(alias)
		}
	}
	return tool
}

// windowsTerminalVersion is Windows Terminal's version, read from the alias
// at alias ("" to skip it) or else from the package folder names, or "" when
// neither says.
func (d *Detector) windowsTerminalVersion(alias string) string {
	if alias != "" {
		if target, err := d.readAlias(alias); err == nil {
			if v := wtVersionFromPath(target); v != "" {
				return v
			}
		}
	}
	return d.windowsTerminalFolderVersion()
}

// wtVersionFromPath is the version in the Windows Terminal package folder
// that path is inside, e.g. "1.24.11911.0" for
// C:\Program Files\WindowsApps\Microsoft.WindowsTerminal_1.24.11911.0_x64__8wekyb3d8bbwe\wt.exe,
// or "" when path is not inside one.
func wtVersionFromPath(path string) string {
	for _, part := range strings.FieldsFunc(path, func(r rune) bool { return r == '\\' || r == '/' }) {
		if m := wtPackagePattern.FindStringSubmatch(part); m != nil {
			return m[1]
		}
	}
	return ""
}

// windowsTerminalFolderVersion is the highest version among the Windows
// Terminal package folders under %ProgramFiles%\WindowsApps, or "" when the
// folder cannot be listed or holds none.
func (d *Detector) windowsTerminalFolderVersion() string {
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

// appExecLinkVersion is the only layout of an app execution alias's reparse
// data Windows has shipped.
const appExecLinkVersion = 3

// errNotAppExecLink is returned for reparse data that is not an app
// execution alias Devpit can read.
var errNotAppExecLink = errors.New("tools: not an app execution alias")

// parseAppExecLink reads the exe an app execution alias stands for out of
// the alias's reparse data (the bytes after the 8-byte REPARSE_DATA_BUFFER
// header): a little-endian uint32 version, 3, then NUL-terminated UTF-16
// strings: the package family name, the app's user model ID, and the full
// path of the exe. Anything after those three is ignored.
func parseAppExecLink(data []byte) (string, error) {
	if len(data) < 4 || binary.LittleEndian.Uint32(data) != appExecLinkVersion {
		return "", errNotAppExecLink
	}
	rest := data[4:]
	units := make([]uint16, len(rest)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(rest[2*i:])
	}
	var fields []string
	start := 0
	for i, u := range units {
		if u == 0 {
			fields = append(fields, string(utf16.Decode(units[start:i])))
			start = i + 1
			if len(fields) == 3 {
				break
			}
		}
	}
	if len(fields) < 3 || fields[2] == "" {
		return "", errNotAppExecLink
	}
	return fields[2], nil
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
