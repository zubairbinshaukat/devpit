//go:build windows

package tools_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/zubairbinshaukat/devpit/internal/tools"
)

// TestRunStepTimeoutKillsTheTree is the real repro for finding 3: `cmd /c`
// runs ping.exe as a grandchild of the Go test process, and that grandchild
// inherits RunStep's stdout/stderr pipe handles. exec.CommandContext's
// default cancellation only kills the direct child (cmd.exe), so without the
// Job Object in step_windows.go, ping.exe keeps running for its full 30s,
// keeps the pipe open, and RunStep's `for line := range lines` loop stays
// blocked long past the 200ms timeout — 29s was the measured hang.
//
// With the Job Object, closing it on timeout kills cmd.exe and ping.exe
// together, so RunStep returns within about 200ms plus overhead.
func TestRunStepTimeoutKillsTheTree(t *testing.T) {
	argv := []string{"cmd", "/c", "start", "/b", "ping", "-n", "30", "127.0.0.1"}

	start := time.Now()
	res := tools.RunStep(context.Background(), argv, 200*time.Millisecond, nil)
	wall := time.Since(start)

	if res.OK {
		t.Fatalf("OK = true, want false (should have timed out)")
	}
	if wall >= 2*time.Second {
		t.Fatalf("RunStep took %v, want < 2s (the grandchild ping.exe should have been killed with the job)", wall)
	}
}

// hiddenPing starts, through PowerShell, a hidden ping.exe that does not
// inherit the step's pipes (Start-Process goes through ShellExecute), prints
// its PID, and then runs tail.
const hiddenPing = `$p = Start-Process -WindowStyle Hidden -PassThru ping.exe -ArgumentList '-n','60','127.0.0.1'; Write-Output ('PID=' + $p.Id); `

// runPowerShell runs script as a step and returns the PID it printed.
func runPowerShell(ctx context.Context, t *testing.T, script string, onPID func()) (tools.StepResult, uint32) {
	t.Helper()
	var pid uint32
	res := tools.RunStep(ctx, []string{"powershell", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script}, time.Minute, func(line string) {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "PID=")
		if !ok {
			return
		}
		if n, err := strconv.ParseUint(rest, 10, 32); err == nil {
			pid = uint32(n)
			if onPID != nil {
				onPID()
			}
		}
	})
	return res, pid
}

// alive reports whether process pid is still running, and stops it when
// stop is set so a test never leaves a ping behind.
func alive(t *testing.T, pid uint32, stop bool) bool {
	t.Helper()
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(h) }()
	// A killed job's processes take a moment to be torn down.
	ev, _ := windows.WaitForSingleObject(h, 2000)
	running := ev == uint32(windows.WAIT_TIMEOUT)
	if running && stop {
		_ = windows.TerminateProcess(h, 1)
	}
	return running
}

// Skipping a step (cancelling its context) stops everything it started, not
// only the direct child.
func TestRunStepCancelKillsTheWholeTree(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start := time.Now()
	res, pid := runPowerShell(ctx, t, hiddenPing+`Start-Sleep -Seconds 60`, cancel)
	if pid == 0 {
		t.Fatalf("no PID printed; last lines %q", res.LastLines)
	}
	// Closing the job ends a process with exit code 0; a stopped step must
	// still never read as a success.
	if res.OK || res.ExitCode != -1 {
		t.Errorf("a cancelled step reported {OK:%v ExitCode:%d}, want not OK and -1", res.OK, res.ExitCode)
	}
	if wall := time.Since(start); wall > 30*time.Second {
		t.Errorf("RunStep took %v after cancel", wall)
	}
	if alive(t, pid, true) {
		t.Error("the skipped step's grandchild is still running")
	}
}

// A step that ends on its own leaves running what its installer started on
// purpose, such as the app it just updated, relaunched.
func TestRunStepSuccessLeavesDetachedChildrenRunning(t *testing.T) {
	res, pid := runPowerShell(context.Background(), t, hiddenPing+`exit 0`, nil)
	if pid == 0 {
		t.Fatalf("no PID printed; last lines %q", res.LastLines)
	}
	if !res.OK {
		t.Fatalf("step failed: %+v", res)
	}
	if !alive(t, pid, true) {
		t.Error("a finished step killed the process its installer left running")
	}
}
