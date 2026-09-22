//go:build windows

package tools_test

import (
	"context"
	"testing"
	"time"

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
