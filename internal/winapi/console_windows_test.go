//go:build windows

package winapi

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// ctrlBreakHelperEnv makes this test binary a sleeping child for
// TestCtrlBreakReachesAChildInItsOwnGroup.
const ctrlBreakHelperEnv = "DEVPIT_TEST_CTRLBREAK_CHILD"

// TestCtrlBreakHelperProcess is the child: it sleeps until Ctrl+Break ends
// it (a Go program with no handler exits on CTRL_BREAK_EVENT).
func TestCtrlBreakHelperProcess(t *testing.T) {
	if os.Getenv(ctrlBreakHelperEnv) != "1" {
		t.Skip("helper process only")
	}
	time.Sleep(30 * time.Second)
	os.Exit(7)
}

// Safety rule 80: a child started in its own process group is reached by
// SendCtrlBreak, and the sender (sharing the console) is not.
func TestCtrlBreakReachesAChildInItsOwnGroup(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestCtrlBreakHelperProcess$") // #nosec G204 -- this test binary
	cmd.Env = append(os.Environ(), ctrlBreakHelperEnv+"=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: CreateNewProcessGroup}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	time.Sleep(300 * time.Millisecond) // let the child reach its sleep
	if err := SendCtrlBreak(cmd.Process.Pid); err != nil {
		_ = cmd.Process.Kill()
		<-done
		t.Skip("no console to signal (", err, ")")
	}
	select {
	case <-done:
		if code := cmd.ProcessState.ExitCode(); code == 7 || code == 0 {
			t.Fatalf("child exit %d: it was not ended by the break", code)
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		t.Fatal("the child did not end after Ctrl+Break")
	}
	// Still here: the break did not reach this process.
}

func TestDownloadsFolderIsAbsolute(t *testing.T) {
	p, err := DownloadsFolder()
	if err != nil {
		t.Skip("no Downloads folder for this account:", err)
	}
	if !filepath.IsAbs(p) {
		t.Fatalf("DownloadsFolder() = %q", p)
	}
}
