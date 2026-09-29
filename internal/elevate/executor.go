package elevate

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/winapi"
)

// waitDelay bounds how long cmd.Wait waits for the output pipes to close
// after the command is cancelled. It is the backstop for a descendant that
// outlived the job object (or a machine where no job could be made) and
// still holds the pipe open.
const waitDelay = 3 * time.Second

// Executor runs the two job kinds the worker accepts. [DefaultExecutor] is
// what cmd/devpit/worker.go gives to [Serve]; tests inject a fake so the
// protocol loop can be exercised without touching real processes or files.
type Executor interface {
	// Exec runs argv to completion, streaming each line of its stdout and
	// stderr through onLine as it arrives, and reports the exit code. A
	// non-nil error means the job failed to run at all (could not start,
	// or the OS reported something other than a plain exit code); a
	// process that ran and exited non-zero is reported via exitCode with a
	// nil error.
	Exec(ctx context.Context, argv []string, onLine func(stream, text string)) (exitCode int, err error)
	// Remove deletes path. [Serve] has already checked path is inside an
	// allowed cleanup root before calling this.
	Remove(ctx context.Context, path string) error
}

// DefaultExecutor is the real implementation: it shells out with os/exec
// and deletes with os.RemoveAll. It imports nothing from internal/ui,
// internal/app, Bubble Tea, or lipgloss — the elevated worker has no UI
// code, per docs/safety.md rule 18.
type DefaultExecutor struct{}

type execLine struct {
	stream, text string
}

// Exec implements [Executor].
func (DefaultExecutor) Exec(ctx context.Context, argv []string, onLine func(stream, text string)) (int, error) {
	if len(argv) == 0 {
		return -1, errors.New("elevate: exec requires at least one argument")
	}

	// #nosec G204 -- argv is exactly what the TUI asked the elevated worker
	// to run (e.g. choco upgrade all -y); running an arbitrary command is
	// this job's whole purpose, gated by the caller choosing to elevate.
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	// Cancelling ctx (a cancel request, a timeout, shutdown) must end the
	// whole tree, not just the command Devpit named: choco starts the
	// vendor's setup, which is the process that actually holds the files.
	// The job object does that on Windows; elsewhere it is a no-op and
	// killing the direct child is all there is.
	job, _ := winapi.NewJob() // nil on failure; a nil job is safe below
	defer job.Kill()
	cmd.Cancel = func() error {
		job.Kill()
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = waitDelay
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return -1, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return -1, err
	}
	if err := cmd.Start(); err != nil {
		return -1, err
	}
	_ = job.Assign(cmd.Process.Pid) // best effort, like the plain steps

	lines := make(chan execLine)
	scanDone := make(chan struct{}, 2)
	scan := func(r io.Reader, stream string) {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), maxLineSize)
		for sc.Scan() {
			lines <- execLine{stream, sc.Text()}
		}
		scanDone <- struct{}{}
	}
	go scan(stdout, "stdout")
	go scan(stderr, "stderr")
	go func() {
		<-scanDone
		<-scanDone
		close(lines)
	}()

	for l := range lines {
		if onLine != nil {
			onLine(l.stream, l.text)
		}
	}

	waitErr := cmd.Wait()
	var exitErr *exec.ExitError
	switch {
	case waitErr == nil:
		return 0, nil
	case errors.As(waitErr, &exitErr):
		return exitErr.ExitCode(), nil
	default:
		return -1, waitErr
	}
}

// Remove implements [Executor].
func (DefaultExecutor) Remove(_ context.Context, path string) error {
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}
