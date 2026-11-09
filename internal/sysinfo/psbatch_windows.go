//go:build windows

package sysinfo

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// createNoWindow is CREATE_NO_WINDOW, so PowerShell never flashes a console.
const createNoWindow = 0x08000000

// maxScriptOutput caps what a run may print.
const maxScriptOutput = 4 << 20

// errPowerShellBlocked is a run that could not start or was stopped.
var errPowerShellBlocked = errors.New("PowerShell did not run (it may be blocked on this PC)")

// powerShellPath is Windows PowerShell 5.1 by its full System32 path, so a
// powershell.exe planted on PATH is never the one started.
func powerShellPath() (string, error) {
	sys, err := windows.GetSystemDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(sys, "WindowsPowerShell", "v1.0", "powershell.exe"), nil
}

// runScript runs a constant script on stdin with a time limit, and kills
// the process when ctx ends or the limit passes.
func runScript(ctx context.Context, script string, limit time.Duration) ([]byte, error) {
	exe, err := powerShellPath()
	if err != nil {
		return nil, err
	}
	return runProgram(ctx, exe, psArgs(), script, limit)
}

// psArgs are the fixed arguments. "-Command -" alone runs stdin line by line
// and quietly drops a block that spans lines, so stdin is read whole and run
// as one script block. The script still never touches the command line.
func psArgs() []string {
	return []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "& ([scriptblock]::Create([Console]::In.ReadToEnd()))"}
}

// runProgram is runScript's process half, split so a test can time out a
// program other than PowerShell.
func runProgram(ctx context.Context, exe string, args []string, stdin string, limit time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...) //nolint:gosec // fixed path and a compile-time script
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	cmd.Stdin = strings.NewReader(stdin)
	var out limitedBuffer
	out.max = maxScriptOutput
	cmd.Stdout = &out
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ErrTimeout
		}
		return nil, ctx.Err()
	}
	if err != nil && out.Len() == 0 {
		return nil, errPowerShellBlocked
	}
	return out.Bytes(), nil
}

// limitedBuffer stops keeping output past max bytes.
type limitedBuffer struct {
	bytes.Buffer
	max int
}

// Write implements io.Writer.
func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// ReadReliability runs the admin read of the drives' counters. The elevated
// worker calls it; unelevated it fails with Windows' access-denied text.
func ReadReliability(ctx context.Context) ([]Reliability, error) {
	out, err := runScript(ctx, reliabilityScript, ReliabilityLimit)
	if err != nil {
		return nil, err
	}
	return parseReliability(out)
}
