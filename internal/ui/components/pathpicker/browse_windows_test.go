package pathpicker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestParseBrowseOutputTrimsWhateverPowerShellAdds covers the shapes the
// script's stdout actually takes: nothing for a cancel, and a path with the
// trailing CRLF PowerShell's pipeline can add.
func TestParseBrowseOutputTrimsWhateverPowerShellAdds(t *testing.T) {
	cases := map[string]string{
		"":                     "",
		"\r\n":                 "",
		`D:\work`:              `D:\work`,
		"D:\\work\r\n":         `D:\work`,
		"  D:\\work  \r\n":     `D:\work`,
		"D:\\work with space#": "D:\\work with space#",
	}
	for in, want := range cases {
		if got := parseBrowseOutput([]byte(in)); got != want {
			t.Errorf("parseBrowseOutput(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestClassifyBrowseErrReportsTimeoutDistinctly is the watchdog: a run that
// was killed because it outlived browseTimeout must produce ErrBrowseTimeout
// — the "did not respond" wording — not the generic ErrBrowseFailed, since
// the two mean different things to a user deciding whether to try again.
func TestClassifyBrowseErrReportsTimeoutDistinctly(t *testing.T) {
	err := classifyBrowseErr(context.DeadlineExceeded, context.DeadlineExceeded, "")
	if !errors.Is(err, ErrBrowseTimeout) {
		t.Errorf("classifyBrowseErr on a deadline = %v, want ErrBrowseTimeout", err)
	}
}

// TestClassifyBrowseErrFoldsInStderr checks that a launch failure carries
// whatever PowerShell put on stderr, so the message is more than "exit
// status 1" when something is actually wrong with the environment.
func TestClassifyBrowseErrFoldsInStderr(t *testing.T) {
	runErr := errors.New("exit status 1")
	err := classifyBrowseErr(runErr, nil, "  Add-Type : some assembly problem  ")
	if !errors.Is(err, ErrBrowseFailed) {
		t.Errorf("classifyBrowseErr = %v, want it to wrap ErrBrowseFailed", err)
	}
	if !strings.Contains(err.Error(), "Add-Type : some assembly problem") {
		t.Errorf("stderr was not folded into the error: %v", err)
	}
}

// TestClassifyBrowseErrWithoutStderr checks the plain-failure path still
// reports something rather than an empty message.
func TestClassifyBrowseErrWithoutStderr(t *testing.T) {
	runErr := errors.New("exit status 1")
	err := classifyBrowseErr(runErr, nil, "")
	if !errors.Is(err, ErrBrowseFailed) || !errors.Is(err, runErr) {
		t.Errorf("classifyBrowseErr = %v, want it to wrap both ErrBrowseFailed and the run error", err)
	}
}

// TestRunPowerShellScriptMechanics proves the plumbing [browse] depends on —
// launching PowerShell hidden, capturing stdout separately from stderr, and
// surfacing a normal exit — using a real PowerShell process and a script
// that never calls ShowDialog. That keeps it safe to run in CI: nothing
// shows a window or waits on a person, but it still exercises the exact
// process-launch path the folder browser uses.
func TestRunPowerShellScriptMechanics(t *testing.T) {
	if _, err := exec.LookPath("powershell.exe"); err != nil {
		t.Skip("powershell.exe not on PATH")
	}

	const script = `[Console]::Out.Write('D:\fixed\path')` + "\n" + `[Console]::Error.Write('a warning')`

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	stdout, stderr, err := runPowerShellScript(ctx, script, nil)
	if err != nil {
		t.Fatalf("runPowerShellScript returned an error: %v", err)
	}
	if got := parseBrowseOutput(stdout); got != `D:\fixed\path` {
		t.Errorf("stdout = %q, want D:\\fixed\\path", got)
	}
	if !strings.Contains(string(stderr), "a warning") {
		t.Errorf("stderr = %q, want it to contain the warning", stderr)
	}
}

// TestRunPowerShellScriptPassesEnv proves DEVPIT_BROWSE_START — how [browse]
// hands the picker's current row to the dialog as a starting folder — makes
// it into the child process without being spliced into the script text.
func TestRunPowerShellScriptPassesEnv(t *testing.T) {
	if _, err := exec.LookPath("powershell.exe"); err != nil {
		t.Skip("powershell.exe not on PATH")
	}

	const script = `[Console]::Out.Write($env:DEVPIT_BROWSE_START)`

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	stdout, _, err := runPowerShellScript(ctx, script, []string{`DEVPIT_BROWSE_START=D:\work`})
	if err != nil {
		t.Fatalf("runPowerShellScript returned an error: %v", err)
	}
	if got := parseBrowseOutput(stdout); got != `D:\work` {
		t.Errorf("stdout = %q, want D:\\work", got)
	}
}

// TestRunPowerShellScriptTimesOut proves a script that never returns is
// killed at the context deadline rather than hanging the caller — the other
// half of the watchdog, below the picker's busy flag.
func TestRunPowerShellScriptTimesOut(t *testing.T) {
	if _, err := exec.LookPath("powershell.exe"); err != nil {
		t.Skip("powershell.exe not on PATH")
	}

	const script = `Start-Sleep -Seconds 60`

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	_, _, err := runPowerShellScript(ctx, script, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a script that outlives the context returned no error")
	}
	if elapsed > 10*time.Second {
		t.Fatalf("runPowerShellScript took %s to return after a 2s deadline", elapsed)
	}
}

// TestBrowseIntegration launches the real folder browser dialog and waits
// for a human to pick a folder or cancel. It is gated behind
// DEVPIT_BROWSE_INTEGRATION=1 because it cannot run headlessly or in CI —
// nothing here can drive the dialog's UI — and exists so a developer can
// confirm the real thing end to end after touching this file.
func TestBrowseIntegration(t *testing.T) {
	if os.Getenv("DEVPIT_BROWSE_INTEGRATION") != "1" {
		t.Skip("set DEVPIT_BROWSE_INTEGRATION=1 to run this against the real dialog")
	}
	path, err := browse("")
	t.Logf("browse() = %q, err = %v", path, err)
	if err != nil {
		t.Fatalf("browse() returned an error: %v", err)
	}
}
