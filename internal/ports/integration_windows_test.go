//go:build windows

package ports

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestByPortFindsOwnListener opens a real TCP listener on an ephemeral port
// and asserts ByPort finds this test binary's own PID with state LISTEN,
// and that Lookup resolves it back to the test binary's name.
func TestByPortFindsOwnListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer ln.Close()

	port := uint16(ln.Addr().(*net.TCPAddr).Port)
	selfPID := uint32(os.Getpid())

	conns, err := ByPort(port)
	if err != nil {
		t.Fatalf("ByPort(%d): %v", port, err)
	}

	var found *Conn
	for i := range conns {
		if conns[i].PID == selfPID && conns[i].State == "LISTEN" {
			found = &conns[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("ByPort(%d) did not find this process (PID %d) in LISTEN state; got %+v", port, selfPID, conns)
	}

	proc, err := Lookup(selfPID)
	if err != nil {
		t.Fatalf("Lookup(%d): %v", selfPID, err)
	}
	if proc.Name == "" {
		t.Error("Lookup() returned an empty process name for the running test binary")
	}
	if proc.Exe == "" {
		t.Error("Lookup() returned an empty exe path for the running test binary")
	}
}

// TestKillRefusesProtectedPIDs pins safety.md rule 16: PID 0 and 4 are never
// killed, and Devpit says why.
func TestKillRefusesProtectedPIDs(t *testing.T) {
	for _, pid := range []uint32{0, 4} {
		err := Kill(pid)
		if err == nil {
			t.Fatalf("Kill(%d) = nil error, want *ProtectedError", pid)
		}
		var protErr *ProtectedError
		if !errors.As(err, &protErr) {
			t.Fatalf("Kill(%d) error = %v (%T), want *ProtectedError", pid, err, err)
		}
		if protErr.Reason == "" {
			t.Errorf("Kill(%d): ProtectedError has no reason", pid)
		}
	}
}

// TestKillTerminatesAChildProcess starts a process this test owns (a
// harmless, long-running ping loop) and confirms Kill actually ends it.
// Devpit must never kill anything it did not start; this is why the target
// is spawned by the test itself.
func TestKillTerminatesAChildProcess(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "ping -n 30 127.0.0.1 >nul")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting test child process: %v", err)
	}
	childPID := uint32(cmd.Process.Pid)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	// Give the child a moment to actually be running before killing it.
	time.Sleep(300 * time.Millisecond)

	if err := Kill(childPID); err != nil {
		_ = cmd.Process.Kill() // clean up regardless of test outcome
		t.Fatalf("Kill(%d): %v", childPID, err)
	}

	select {
	case <-done:
		// exited, as expected (exit code from TerminateProcess is non-zero,
		// which is fine -- we only care that it exited).
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("child process %d did not exit within 10s of Kill", childPID)
	}
}

// TestWaitFreeAfterListenerCloses proves WaitFree reports a port free once
// nothing is LISTENing on it anymore.
func TestWaitFreeAfterListenerCloses(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	port := uint16(ln.Addr().(*net.TCPAddr).Port)
	ln.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !WaitFree(ctx, port, 2*time.Second) {
		t.Fatalf("WaitFree(%d) = false after the listener closed", port)
	}
}
