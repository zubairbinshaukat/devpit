package pathpicker

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

// browseSupported reports that this build can open a folder browser.
const browseSupported = true

// browseDesc is the description shown beside the Browse row.
const browseDesc = "Open the Windows folder browser"

// browseTimeout is how long Devpit waits for the dialog before giving up. A
// user who wanders off leaves a hidden PowerShell process behind otherwise.
const browseTimeout = 5 * time.Minute

// ErrBrowseFailed means the folder browser could not be opened.
var ErrBrowseFailed = errors.New("the folder browser could not be opened — type the path instead")

// ErrBrowseTimeout means the dialog did not return within [browseTimeout].
// It is a distinct error from ErrBrowseFailed so the picker can say the
// browser is unresponsive rather than that it failed outright.
var ErrBrowseTimeout = errors.New("the folder browser did not respond — type the path instead")

// browseScript is the dialog, as a PowerShell one-liner.
//
// The dialog is Windows Forms' FolderBrowserDialog, driven from PowerShell
// rather than from Go. The alternative is SHBrowseForFolderW or IFileDialog
// through syscall, and both of those need a single-threaded apartment, a
// COM initialisation and a message pump on a locked OS thread; that is a
// large amount of hand-rolled COM to own for one optional convenience in a
// program that otherwise never leaves the terminal. PowerShell is present on
// every supported Windows version, the call is a fixed string with no user
// input interpolated into it, and a failure is a message rather than a
// crash: the user can always type the path.
//
// ShowDialog is given an owner so the window comes to the front instead of
// appearing behind the terminal.
const browseScript = `
Add-Type -AssemblyName System.Windows.Forms | Out-Null
$d = New-Object System.Windows.Forms.FolderBrowserDialog
$d.Description = 'Choose the folder Devpit should scan'
$d.ShowNewFolderButton = $false
$d.RootFolder = [System.Environment+SpecialFolder]::MyComputer
if ($env:DEVPIT_BROWSE_START) { $d.SelectedPath = $env:DEVPIT_BROWSE_START }
$owner = New-Object System.Windows.Forms.Form
$owner.TopMost = $true
if ($d.ShowDialog($owner) -eq [System.Windows.Forms.DialogResult]::OK) {
  [Console]::Out.Write($d.SelectedPath)
}
$owner.Dispose()
`

// browse opens the Windows folder browser and returns the chosen path, or
// "" when the user cancelled.
//
// start is passed through the environment rather than spliced into the
// script, so nothing a path contains can ever be read as PowerShell.
//
// stdout and stderr are captured into separate buffers rather than through
// cmd.Output(): Add-Type and WinForms can write warnings to stderr on a
// success path, and folding that into the error text would make a working
// pick look like a failure. The window is hidden (SysProcAttr.HideWindow)
// so launching PowerShell for the dialog never flashes a console behind it.
func browse(start string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), browseTimeout)
	defer cancel()

	var env []string
	if start != "" && !IsUNC(start) {
		env = []string{"DEVPIT_BROWSE_START=" + start}
	}

	stdout, stderr, err := runPowerShellScript(ctx, browseScript, env)
	if err != nil {
		return "", classifyBrowseErr(err, ctx.Err(), string(stderr))
	}
	return parseBrowseOutput(stdout), nil
}

// runPowerShellScript runs script under a hidden, non-interactive,
// single-threaded-apartment PowerShell and returns its captured stdout and
// stderr separately. It is the mechanics [browse] relies on — process
// launch, a hidden window, output capture and exit-code handling — factored
// out so a test can exercise them against a script that never shows a
// dialog.
func runPowerShellScript(ctx context.Context, script string, extraEnv []string) (stdout, stderr []byte, err error) {
	exe := powershellExe()
	cmd := exec.CommandContext(ctx, exe,
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
// the picker shows. A context deadline is reported as ErrBrowseTimeout, the
// wording the picker's watchdog uses, rather than the generic failure —
// "did not respond" is a truer description than "could not be opened" when
// the process was killed for running past browseTimeout.
func classifyBrowseErr(runErr, ctxErr error, stderr string) error {
	if errors.Is(ctxErr, context.DeadlineExceeded) {
		return ErrBrowseTimeout
	}
	stderr = strings.TrimSpace(stderr)
	if stderr != "" {
		return fmt.Errorf("%w: %w: %s", ErrBrowseFailed, runErr, stderr)
	}
	return fmt.Errorf("%w: %w", ErrBrowseFailed, runErr)
}

// parseBrowseOutput turns the script's stdout into the chosen path. The
// script writes nothing when the user cancels, so a blank result — after
// trimming the CRLF PowerShell's pipeline can add — means cancellation, not
// an empty folder.
func parseBrowseOutput(out []byte) string {
	return strings.TrimSpace(string(out))
}

// powershellExe prefers PowerShell 7 when it is on PATH and falls back to
// Windows PowerShell, which every supported Windows has.
func powershellExe() string {
	if p, err := exec.LookPath("pwsh.exe"); err == nil {
		return p
	}
	return "powershell.exe"
}
