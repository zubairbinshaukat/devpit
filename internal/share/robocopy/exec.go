package robocopy

import (
	"context"
	"errors"
	"io"
	"os/exec"
)

// ExecRunner is the real [Runner]: it runs robocopy.exe from the system
// path, with its console output thrown away because the log file holds what
// matters. The process gets no window.
type ExecRunner struct{}

// Run implements [Runner]. Cancelling ctx kills robocopy, which leaves any
// half-copied file in restartable form thanks to /Z.
func (ExecRunner) Run(ctx context.Context, args []string) (int, error) {
	// #nosec G204 -- args come from CopyArgs and DryRunArgs, built from paths
	// the user chose and validated, and there is no shell to interpret them.
	cmd := exec.CommandContext(ctx, "robocopy", args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	hideWindow(cmd)
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &exitErr):
		if ctx.Err() != nil {
			return exitErr.ExitCode(), ctx.Err()
		}
		return exitErr.ExitCode(), nil
	default:
		return -1, err
	}
}
