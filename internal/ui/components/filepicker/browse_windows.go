package filepicker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// browseSupported reports that this build can open a file picker.
const browseSupported = true

// browseTimeout is how long Devpit waits for the dialog before giving up. A
// user who wanders off leaves a hidden PowerShell process behind otherwise.
const browseTimeout = 5 * time.Minute

// errBrowseFailed means the file picker could not be opened.
var errBrowseFailed = errors.New("the file picker could not be opened")

// errBrowseTimeout means the dialog did not return within [browseTimeout].
var errBrowseTimeout = errors.New("the file picker did not respond")

// browseStartEnv carries the folder the dialog opens in.
const browseStartEnv = "DEVPIT_BROWSE_START"

// browseScript is the dialog, as a PowerShell script.
//
// It is Windows Forms' OpenFileDialog driven from PowerShell, for the reason
// pathpicker gives for its folder browser: the native dialogs need a
// single-threaded apartment, COM and a message pump on a locked OS thread,
// which is a lot of hand-rolled COM for one optional convenience. The script
// is a fixed string. Nothing the user typed and no path is spliced into it:
// the start folder arrives through the environment.
//
// The output encoding is set to UTF-8 so a path with letters outside the
// console's code page comes back intact. ShowDialog is given an owner so the
// window comes to the front instead of appearing behind the terminal.
const browseScript = `
Add-Type -AssemblyName System.Windows.Forms | Out-Null
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$d = New-Object System.Windows.Forms.OpenFileDialog
$d.Title = 'Choose the archive Devpit should extract'
$d.Filter = 'Archives|*.zip;*.7z;*.rar;*.001;*.r00|All files|*.*'
$d.CheckFileExists = $true
$d.Multiselect = $false
if ($env:DEVPIT_BROWSE_START) { $d.InitialDirectory = $env:DEVPIT_BROWSE_START }
$owner = New-Object System.Windows.Forms.Form
$owner.TopMost = $true
if ($d.ShowDialog($owner) -eq [System.Windows.Forms.DialogResult]::OK) {
  [Console]::Out.Write($d.FileName)
}
$owner.Dispose()
`

// browse opens the Windows file picker and returns the chosen file, or ""
// when the user cancelled.
func browse(start string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), browseTimeout)
	defer cancel()

	var env []string
	if start != "" && !isUNC(start) {
		env = []string{browseStartEnv + "=" + start}
	}

	stdout, stderr, err := runPowerShellScript(ctx, browseScript, env)
	if err != nil {
		return "", classifyBrowseErr(err, ctx.Err(), string(stderr))
	}
	return parseBrowseOutput(stdout), nil
}

// isUNC reports a \\server\share path, which is never handed to the dialog
// as its start folder.
func isUNC(p string) bool {
	return len(p) >= 2 && (p[0] == '\\' || p[0] == '/') && (p[1] == '\\' || p[1] == '/')
}

// runPowerShellScript runs script under a hidden, non-interactive,
// single-threaded-apartment PowerShell and returns its stdout and stderr
// separately, so a warning on stderr never turns a good pick into a failure.
func runPowerShellScript(ctx context.Context, script string, extraEnv []string) (stdout, stderr []byte, err error) {
	// #nosec G204 -- a fixed program and a fixed script; no input is spliced in.
	cmd := exec.CommandContext(ctx, powershellExe(),
		"-NoProfile", "-NonInteractive", "-STA",
		"-ExecutionPolicy", "Bypass",
		"-Command", script,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if len(extraEnv) > 0 {
		cmd.Env = append(cmd.Environ(), extraEnv...)
	}

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	if runErr := cmd.Run(); runErr != nil {
		return nil, errBuf.Bytes(), runErr
	}
	return outBuf.Bytes(), errBuf.Bytes(), nil
}

// classifyBrowseErr turns a failed run of the dialog script into the error
// the picker words: a passed deadline is "did not respond", anything else
// "could not be opened".
func classifyBrowseErr(runErr, ctxErr error, stderr string) error {
	if errors.Is(ctxErr, context.DeadlineExceeded) {
		return errBrowseTimeout
	}
	if stderr = strings.TrimSpace(stderr); stderr != "" {
		return fmt.Errorf("%w: %w: %s", errBrowseFailed, runErr, stderr)
	}
	return fmt.Errorf("%w: %w", errBrowseFailed, runErr)
}

// parseBrowseOutput turns the script's stdout into the chosen path. The
// script writes nothing on a cancel, so a blank result means cancellation.
// A UTF-8 byte order mark, which some hosts put in front, is dropped.
func parseBrowseOutput(out []byte) string {
	return strings.TrimSpace(strings.TrimPrefix(string(out), "\uFEFF"))
}

// powershellExe prefers PowerShell 7 when it is on PATH and falls back to
// Windows PowerShell, which every supported Windows has.
func powershellExe() string {
	if p, err := exec.LookPath("pwsh.exe"); err == nil {
		return p
	}
	return "powershell.exe"
}
