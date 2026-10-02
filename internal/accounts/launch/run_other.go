//go:build !windows

package launch

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// batchCommand refuses: a .cmd or .bat only runs on Windows.
func batchCommand(context.Context, string, []string) (*exec.Cmd, error) {
	return nil, fmt.Errorf("%w: .cmd and .bat programs only run on Windows", ErrUnsafeArgument)
}

// Run starts the program and waits for it, sharing the terminal, and
// returns its exit code. Job objects and console control events are Windows
// things; elsewhere this is a plain start and wait.
func Run(spec Spec) (int, error) {
	c, err := spec.command()
	if err != nil {
		return 1, err
	}
	if err := c.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), nil
		}
		return 1, err
	}
	return 0, nil
}
