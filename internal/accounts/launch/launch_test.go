package launch

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// hostileArgs is the spike's table (plans/v0.4.0-spikes.md §3) plus a few
// more: each must arrive exactly, or be refused, and never run anything.
func hostileArgs(marker string) []string {
	return []string{
		"a b", "a&echo INJECTED>" + marker, "a|findstr x", `"`, `a"b`, `C:\dir\`, "",
		"héllo-日本-ü", "!PATH!", "(x)", "%dp0%", "%PATH%", "^", "a^b", "<x>",
		`"&echo INJECTED2>` + marker + `&"`, `\\"`, `a\\`, "100%", "a,b;c=d", "tab\there",
	}
}

func TestBatchCommandLineRefusesWhatCmdWouldChange(t *testing.T) {
	cmdExe := `C:\Windows\system32\cmd.exe`
	for _, a := range hostileArgs(`C:\x\pwned.txt`) {
		_, err := BatchCommandLine(cmdExe, `C:\tools\x.cmd`, []string{a})
		if SafeBatchArg(a) {
			if err != nil {
				t.Errorf("safe arg %q refused: %v", a, err)
			}
			continue
		}
		if !errors.Is(err, ErrUnsafeArgument) {
			t.Errorf("unsafe arg %q not refused: %v", a, err)
		}
	}
	for _, bad := range []string{`C:\100%\x.cmd`, `C:\a"b\x.cmd`, `C:\a&b\x.cmd`, `C:\x\`} {
		if _, err := BatchCommandLine(cmdExe, bad, nil); !errors.Is(err, ErrUnsafeArgument) {
			t.Errorf("script %q accepted", bad)
		}
	}
	line, err := BatchCommandLine(cmdExe, `C:\Program Files\x\x.cmd`, []string{"deploy", "--prod", "a b", "", `C:\dir\`, "k=v", "x@y.com"})
	if err != nil {
		t.Fatal(err)
	}
	want := `"C:\Windows\system32\cmd.exe" /d /e:ON /v:OFF /s /c ""C:\Program Files\x\x.cmd" deploy --prod "a b" "" "C:\dir\\" "k=v" x@y.com"`
	if line != want {
		t.Fatalf("line:\n%s\nwant:\n%s", line, want)
	}
}

const npmShim = `@ECHO off
GOTO start
:find_dp0
SET dp0=%~dp0
EXIT /b
:start
SETLOCAL
CALL :find_dp0

IF EXIST "%dp0%\node.exe" (
  SET "_prog=%dp0%\node.exe"
) ELSE (
  SET "_prog=node"
  SET PATHEXT=%PATHEXT:;.JS;=;%
)

endLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & "%_prog%"  "%dp0%\node_modules\vercel\dist\vc.js" %*
`

const oldNpmShim = `@IF EXIST "%~dp0\node.exe" (
  "%~dp0\node.exe"  "%~dp0\node_modules\npm\bin\npm-cli.js" %*
) ELSE (
  @SETLOCAL
  @SET PATHEXT=%PATHEXT:;.JS;=;%
  node  "%~dp0\node_modules\npm\bin\npm-cli.js" %*
)
`

func TestParseNpmShim(t *testing.T) {
	got, ok := parseNpmShim([]byte(strings.ReplaceAll(npmShim, "\n", "\r\n")), `C:\Users\z\AppData\Roaming\npm`)
	if !ok || len(got) != 1 || got[0] != `C:\Users\z\AppData\Roaming\npm\node_modules\vercel\dist\vc.js` {
		t.Fatalf("current shim: %v %v", got, ok)
	}
	got, ok = parseNpmShim([]byte(oldNpmShim), `D:\npm\`)
	if !ok || len(got) != 1 || got[0] != `D:\npm\node_modules\npm\bin\npm-cli.js` {
		t.Fatalf("old shim: %v %v", got, ok)
	}
	for _, other := range []string{
		"@echo off\r\necho hello %*\r\n",
		`"%_prog%" "%dp0%\x.js" %OTHER% %*`,
		`"%_prog%" "%dp0%\x.js & calc" %*`,
		`"%_prog%" "%dp0%\x.js %*`,
		"",
	} {
		if got, ok := parseNpmShim([]byte(other), `C:\x`); ok {
			t.Errorf("not an npm shim, but parsed: %q → %v", other, got)
		}
	}
}

func TestFindOrderAndSkips(t *testing.T) {
	root := t.TempDir()
	shim, a, b := filepath.Join(root, "shims"), filepath.Join(root, "a"), filepath.Join(root, "b")
	for _, d := range []string{shim, a, b} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	touch := func(p string) {
		t.Helper()
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	touch(filepath.Join(shim, "tool.exe"))
	touch(filepath.Join(a, "tool.cmd"))
	touch(filepath.Join(b, "tool.exe"))
	touch(filepath.Join(a, "other.cmd"))
	touch(filepath.Join(a, "other.exe"))
	touch(filepath.Join(a, "script.ps1"))
	if err := os.MkdirAll(filepath.Join(b, "dir.exe"), 0o700); err != nil {
		t.Fatal(err)
	}
	sep := string(os.PathListSeparator)
	path := strings.Join([]string{shim, "relative", `"` + a + `"`, "", b}, sep)

	opts := FindOptions{Path: path, PathExt: ".COM;.EXE;.BAT;.CMD;.PS1", Skip: []string{strings.ToUpper(shim) + string(os.PathSeparator)}}
	got, err := Find("tool", opts)
	if err != nil || got != filepath.Join(a, "tool.cmd") {
		t.Fatalf("Find(tool) = %q, %v; folder order comes first, the shim folder is skipped", got, err)
	}
	if got, _ := Find("other", opts); got != filepath.Join(a, "other.exe") {
		t.Fatalf("Find(other) = %q; .exe comes first within a folder", got)
	}
	if got, _ := Find("tool.exe", opts); got != filepath.Join(b, "tool.exe") {
		t.Fatalf("Find(tool.exe) = %q", got)
	}
	if _, err := Find("script", opts); !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("a .ps1 is never started: %v", err)
	}
	if _, err := Find("dir", opts); !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("a folder is not a program: %v", err)
	}
	// Without the skip, the shim folder comes first; with Self, the shim
	// itself is skipped wherever it is.
	if got, _ := Find("tool", FindOptions{Path: path}); got != filepath.Join(shim, "tool.exe") {
		t.Fatalf("no skip: %q", got)
	}
	if got, _ := Find("tool", FindOptions{Path: path, Self: filepath.Join(shim, "tool.exe")}); got != filepath.Join(a, "tool.cmd") {
		t.Fatalf("self skip: %q", got)
	}
	if _, err := Find(`..\x`, opts); err == nil {
		t.Fatal("a path is not a program name")
	}
}

func TestMergeEnv(t *testing.T) {
	base := []string{"=C:=C:\\x", "Path=C:\\bin", "CLAUDE_CONFIG_DIR=old", "SUPABASE_ACCESS_TOKEN=x", "KEEP=1"}
	got := MergeEnv(base, []string{"claude_config_dir=new"}, []string{"supabase_access_token"})
	want := []string{"=C:=C:\\x", "Path=C:\\bin", "KEEP=1", "claude_config_dir=new"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v", got)
	}
}
