//go:build windows

package winapi

import (
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestJobHelperProcess is not a test: it is the child that
// TestKilledJobProcessesExitWithANonZeroCode starts and kills.
func TestJobHelperProcess(t *testing.T) {
	if os.Getenv("DEVPIT_JOB_HELPER") != "1" {
		t.Skip("helper process only")
	}
	time.Sleep(time.Minute)
}

// TestKilledJobProcessesExitWithANonZeroCode pins the fix for a killed
// process that reported success: closing a kill-on-close job ends its
// processes with exit code 0, which callers read as "the installer
// finished". Kill must give them a non-zero code.
func TestKilledJobProcessesExitWithANonZeroCode(t *testing.T) {
	job, err := NewJob()
	if err != nil {
		t.Fatal(err)
	}
	defer job.Kill()
	cmd := exec.Command(os.Args[0], "-test.run=^TestJobHelperProcess$") // #nosec G204 -- this test binary
	cmd.Env = append(os.Environ(), "DEVPIT_JOB_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := job.Assign(cmd.Process.Pid); err != nil {
		_ = cmd.Process.Kill()
		t.Fatal(err)
	}
	job.Kill()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != KilledExitCode {
			t.Errorf("killed process ended with %v, want exit code %d", err, KilledExitCode)
		}
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the process in the job was not killed")
	}
	job.Kill() // a second Kill is harmless
}
