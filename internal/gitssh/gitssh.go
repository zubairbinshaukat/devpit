// Package gitssh provides Git identity configuration, SSH key generation
// and clipboard helpers for the Git & SSH Setup screen. It never imports
// Bubble Tea or any UI package.
//
// Every external process this package runs (git, ssh-keygen) goes through
// the injectable [Runner] interface so tests never exec a real binary or
// touch the real ~/.ssh.
package gitssh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// Runner runs an external command and returns its combined stdout+stderr
// output. Implementations must respect ctx cancellation.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (output string, err error)
}

// Options configures the package's exported functions that shell out to an
// external process (git, ssh-keygen).
type Options struct {
	// Runner runs the external command. A nil Runner uses the real
	// os/exec-backed implementation.
	Runner Runner
}

func (o Options) runner() Runner {
	if o.Runner != nil {
		return o.Runner
	}
	return execRunner{}
}

// ExitError reports the exit code of a command that ran but exited
// non-zero. Fakes in tests can construct it directly; the real [Runner]
// wraps any *exec.ExitError it sees into one.
type ExitError struct {
	Code   int
	Stderr string
}

func (e *ExitError) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("exit status %d: %s", e.Code, e.Stderr)
	}
	return fmt.Sprintf("exit status %d", e.Code)
}

// NotInstalledError reports that the external tool (git or ssh-keygen)
// could not be found on PATH.
type NotInstalledError struct {
	Tool string
	Err  error
}

func (e *NotInstalledError) Error() string {
	return fmt.Sprintf("gitssh: %s is not installed or not on PATH: %v", e.Tool, e.Err)
}

func (e *NotInstalledError) Unwrap() error { return e.Err }

// execRunner is the real [Runner], backed by os/exec.
type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // name/args are internal constants (git, ssh-keygen), never user input.

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		return stdout.String(), nil
	}

	if errors.Is(err, exec.ErrNotFound) {
		return stdout.String(), err
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return stdout.String(), &ExitError{Code: exitErr.ExitCode(), Stderr: stderr.String()}
	}
	return stdout.String(), err
}
