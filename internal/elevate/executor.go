package elevate

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync/atomic"
	"time"
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

// maxExecLine is the longest piece of one output line sent as one event.
// A longer line is split rather than dropped, and the bound keeps even a
// line of control characters (six bytes each once JSON-escaped) under the
// codec's [maxLineSize], so no command's output can break the pipe.
const maxExecLine = 128 * 1024

// splitExecLines is bufio.ScanLines, except that a line longer than
// maxExecLine comes out in maxExecLine pieces instead of stopping the scan
// (a scanner that stops reading leaves the command blocked on a full pipe).
func splitExecLines(data []byte, atEOF bool) (int, []byte, error) {
	if adv, tok, err := bufio.ScanLines(data, atEOF); adv > 0 || tok != nil || err != nil {
		return adv, tok, err
	}
	if len(data) >= maxExecLine {
		return maxExecLine, data[:maxExecLine], nil
	}
	return 0, nil, nil
}

// Exec implements [Executor].
//
// Cancelling ctx (a cancel request, a timeout, shutdown) ends the whole
// process tree, not just the command Devpit named (choco starts the vendor's
// setup, which is the process that actually holds the files), and is
// reported as the context's error, never as an exit code: a process ended
// through its job can exit with code 0, which must not read as success. A
// command that ends on its own leaves alone whatever it started and meant to
// keep running, such as the updated app relaunched.
func (DefaultExecutor) Exec(ctx context.Context, argv []string, onLine func(stream, text string)) (int, error) {
	if len(argv) == 0 {
		return -1, errors.New("elevate: exec requires at least one argument")
	}

	// #nosec G204 -- argv is exactly what the TUI asked the elevated worker
	// to run (e.g. choco upgrade all -y); running an arbitrary command is
	// this job's whole purpose, gated by the caller choosing to elevate.
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	tree := newJobTree()
	var stopped atomic.Bool
	defer func() {
		if stopped.Load() || ctx.Err() != nil {
			tree.kill()
			return
		}
		tree.release()
	}()
	cmd.Cancel = func() error {
		stopped.Store(true)
		tree.kill()
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
	_ = tree.assign(cmd.Process.Pid) // best effort, like the plain steps

	lines := make(chan execLine)
	scanDone := make(chan struct{}, 2)
	scan := func(r io.Reader, stream string) {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), maxExecLine)
		sc.Split(splitExecLines)
		for sc.Scan() {
			lines <- execLine{stream, sc.Text()}
		}
		// A read error ends the scan; keep draining so the command is never
		// left blocked on a full pipe.
		_, _ = io.Copy(io.Discard, r)
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
	if stopped.Load() || ctx.Err() != nil {
		if err := ctx.Err(); err != nil {
			return -1, err
		}
		return -1, errors.New("stopped")
	}
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

// Remove implements [Executor]. It deletes through an [os.Root] opened on
// the allowed cleanup root, so a junction or symbolic link planted inside
// that root (%WINDIR%\Temp is writable by every user) cannot steer an
// elevated delete to a folder outside it: os.Root refuses any path that
// resolves outside the root, where a plain os.RemoveAll would follow a
// junction in a parent folder and delete wherever it points.
func (DefaultExecutor) Remove(_ context.Context, path string) error {
	root, rel, err := removeRoot(path)
	if err != nil {
		return err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	defer r.Close() //nolint:errcheck // a read-only directory handle
	if err := r.RemoveAll(rel); err != nil {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}
