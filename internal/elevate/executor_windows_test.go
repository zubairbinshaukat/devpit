//go:build windows

package elevate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests start ordinary, unelevated cmd.exe and ping.exe processes on
// the loopback address. Nothing is elevated or changed on the system.

// A command stopped by its context is reported as stopped, never with an
// exit code that could read as success: a process ended through its job
// object can exit with code 0.
func TestAStoppedCommandIsNeverASuccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	code, err := DefaultExecutor{}.Exec(ctx, []string{"cmd.exe", "/c", "ping -n 30 127.0.0.1"}, nil)
	if err == nil || code != -1 {
		t.Fatalf("Exec = %d, %v; want -1 and the context's error", code, err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("the stop took %v", d)
	}
}

// A command that finishes on its own leaves running what it started on
// purpose (an installer relaunching the updated app); only a stopped
// command takes its tree with it.
func TestAFinishedCommandLeavesWhatItStartedRunning(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "still-running.txt")
	// start /b launches a second cmd with its input and output on nul, so it
	// does not hold Exec's pipes; it writes the marker about two seconds
	// after the first cmd has exited. A batch file keeps cmd's quoting away
	// from Go's.
	batch := filepath.Join(dir, "spawn.cmd")
	body := "@echo off\r\nstart \"\" /b cmd /c \"ping -n 3 127.0.0.1 >nul & echo x>" + marker + "\" <nul >nul 2>nul\r\n"
	if err := os.WriteFile(batch, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	code, err := DefaultExecutor{}.Exec(context.Background(), []string{"cmd.exe", "/c", batch}, nil)
	if err != nil || code != 0 {
		t.Fatalf("Exec = %d, %v", code, err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the process the command started was killed when the command finished")
}

// A line far longer than the pipe codec allows comes through in pieces
// instead of stopping the output (and blocking the command on a full pipe).
func TestAHugeOutputLineIsSplitNotDropped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// 300,000 characters on one line, then a normal one.
	script := `$s='x'*300000; [Console]::Out.Write($s); [Console]::Out.WriteLine(); 'after'`
	var pieces []string
	code, err := DefaultExecutor{}.Exec(ctx, []string{psExe(), "-NoProfile", "-NonInteractive", "-Command", script},
		func(_, text string) { pieces = append(pieces, text) })
	if err != nil || code != 0 {
		t.Fatalf("Exec = %d, %v", code, err)
	}
	total := 0
	for _, p := range pieces {
		if len(p) > maxExecLine {
			t.Errorf("a piece of %d bytes is over the %d limit", len(p), maxExecLine)
		}
		total += strings.Count(p, "x")
	}
	if total != 300000 || pieces[len(pieces)-1] != "after" {
		t.Errorf("got %d x's in %d pieces, last %q; want all 300000 and then \"after\"", total, len(pieces), pieces[len(pieces)-1])
	}
}
