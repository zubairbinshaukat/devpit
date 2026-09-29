package tools

import (
	"encoding/binary"
	"errors"
	"testing"
	"unicode/utf16"
)

// appExecLinkData builds an alias's reparse data (without the 8-byte header)
// the way Windows lays it out: version, then NUL-terminated UTF-16 strings.
func appExecLinkData(version uint32, fields ...string) []byte {
	out := binary.LittleEndian.AppendUint32(nil, version)
	for _, f := range fields {
		for _, u := range utf16.Encode([]rune(f)) {
			out = binary.LittleEndian.AppendUint16(out, u)
		}
		out = binary.LittleEndian.AppendUint16(out, 0)
	}
	return out
}

// The reparse data of the real wt.exe alias on a Windows 11 machine, as
// `fsutil reparsepoint query` prints it: package family, app ID, target, and
// the application type "0".
func TestParseAppExecLink(t *testing.T) {
	const target = `C:\Program Files\WindowsApps\Microsoft.WindowsTerminal_1.24.11911.0_x64__8wekyb3d8bbwe\wt.exe`
	data := appExecLinkData(3,
		"Microsoft.WindowsTerminal_8wekyb3d8bbwe",
		"Microsoft.WindowsTerminal_8wekyb3d8bbwe!App",
		target, "0")
	got, err := parseAppExecLink(data)
	if err != nil || got != target {
		t.Fatalf("parseAppExecLink = %q, %v; want %q", got, err, target)
	}
	if v := wtVersionFromPath(got); v != "1.24.11911.0" {
		t.Errorf("wtVersionFromPath = %q, want 1.24.11911.0", v)
	}

	bad := map[string][]byte{
		"empty":         nil,
		"wrong version": appExecLinkData(2, "a", "b", target),
		"two fields":    appExecLinkData(3, "a", "b"),
		"no terminator": appExecLinkData(3, "a", "b")[:10],
		"empty target":  appExecLinkData(3, "a", "b", ""),
	}
	for name, d := range bad {
		if got, err := parseAppExecLink(d); !errors.Is(err, errNotAppExecLink) {
			t.Errorf("%s: parseAppExecLink = %q, %v; want errNotAppExecLink", name, got, err)
		}
	}
}

func TestWTVersionFromPath(t *testing.T) {
	cases := map[string]string{
		`C:\Program Files\WindowsApps\Microsoft.WindowsTerminal_1.21.3231.0_x64__8wekyb3d8bbwe\wt.exe`:         "1.21.3231.0",
		`C:\Program Files\WindowsApps\Microsoft.WindowsTerminal_1.22.1.0_arm64__8wekyb3d8bbwe\wt.exe`:          "1.22.1.0",
		`C:\Program Files\WindowsApps\Microsoft.WindowsTerminalPreview_1.99.1.0_x64__8wekyb3d8bbwe\wt.exe`:     "",
		`C:\Program Files\WindowsApps\Microsoft.WindowsTerminal_3001.24.11911.0_neutral_~_8wekyb3d8bbwe\x.exe`: "",
		`C:\Users\a\scoop\apps\windows-terminal\current\wt.exe`:                                                "",
	}
	for path, want := range cases {
		if got := wtVersionFromPath(path); got != want {
			t.Errorf("wtVersionFromPath(%q) = %q, want %q", path, got, want)
		}
	}
}
