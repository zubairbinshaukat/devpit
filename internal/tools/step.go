package tools

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os/exec"
	"time"
)

// DefaultStepTimeout is used by [RunStep] when the caller passes a
// non-positive timeout. The Update screen runs one package-manager step at a
// time and each step gets up to ten minutes before it is killed.
const DefaultStepTimeout = 10 * time.Minute

// lastLinesKept is how many trailing output lines [StepResult] retains, so a
// failed step's summary card has enough context without buffering the whole
// log.
const lastLinesKept = 20

// errNotStarted is returned by a [processTracker]'s track when cmd.Process
// is nil, i.e. cmd.Start has not been called yet.
var errNotStarted = errors.New("tools: process not started")

// waitDelay bounds how long cmd.Wait waits for the stdout/stderr pipes to
// close after the process tree is killed. It is a backstop for
// processTracker: if the Job Object could not be created (a sandboxed token,
// a policy restriction), the pipe readers would otherwise still be able to
// block forever on a grandchild that inherited the pipe handle.
const waitDelay = 3 * time.Second

// processTracker lets RunStep put a child process — and everything it goes
// on to spawn — under one roof, so a timeout can kill the whole tree rather
// than just the direct child exec.CommandContext knows about.
//
// This matters on Windows specifically: exec.CommandContext's default
// cancellation calls Process.Kill() on the direct child only. A step like
// `cmd /c somepackagemanager ...` runs as a grandchild of that direct child,
// and if it inherited the stdout/stderr pipe (the normal case), it keeps
// that pipe open for as long as it runs, which blocks RunStep's
// `for line := range lines` loop well past the timeout. See step_windows.go
// for the Job Object that fixes this; step_other.go is a no-op since other
// platforms do not have this failure mode for the commands RunStep runs.
type processTracker interface {
	// track assigns cmd's already-started process to the tracker. cmd.Start
	// must have been called first.
	track(cmd *exec.Cmd) error
	// kill terminates every process the tracker knows about. Safe to call
	// more than once, and safe to call even when track was never called or
	// returned an error.
	kill()
}

// StepResult is the outcome of one [RunStep] call.
type StepResult struct {
	// OK is true when the command exited with status 0 before the timeout.
	OK bool
	// ExitCode is the process exit code, or -1 if it never started or was
	// killed by the timeout.
	ExitCode int
	// LastLines holds up to the last 20 lines of combined stdout+stderr,
	// oldest first. Populated whether the step succeeded or failed.
	LastLines []string
	// Elapsed is the wall-clock time the command ran for.
	Elapsed time.Duration
}

// RunStep runs argv as a child process, streaming each line of its combined
// stdout+stderr to onLine as it arrives, and reports how it went. It is used
// by the Update screen to drive one package-manager step at a time.
//
// timeout bounds the whole run; a non-positive timeout falls back to
// [DefaultStepTimeout]. onLine may be nil.
func RunStep(ctx context.Context, argv []string, timeout time.Duration, onLine func(string)) StepResult {
	if timeout <= 0 {
		timeout = DefaultStepTimeout
	}
	if len(argv) == 0 {
		return StepResult{ExitCode: -1}
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()

	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...) //nolint:gosec // argv is built by Manager command builders in this package, never from unsanitized user input.
	cmd.WaitDelay = waitDelay
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return StepResult{ExitCode: -1, Elapsed: time.Since(start)}
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return StepResult{ExitCode: -1, Elapsed: time.Since(start)}
	}

	if err := cmd.Start(); err != nil {
		return StepResult{ExitCode: -1, Elapsed: time.Since(start)}
	}

	// Best-effort: track the process tree so the goroutine below can kill
	// every descendant, not just cmd itself, the moment the timeout fires.
	// A tracking failure (e.g. no permission to create a Job Object) leaves
	// waitDelay above as the only backstop.
	tracker := newProcessTracker()
	_ = tracker.track(cmd)
	defer tracker.kill()

	trackerStop := make(chan struct{})
	defer close(trackerStop)
	go func() {
		select {
		case <-runCtx.Done():
			tracker.kill()
		case <-trackerStop:
		}
	}()

	lines := make(chan string)
	done := make(chan struct{}, 2)
	scan := func(r io.Reader) {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			lines <- sc.Text()
		}
		done <- struct{}{}
	}
	go scan(stdout)
	go scan(stderr)

	go func() {
		<-done
		<-done
		close(lines)
	}()

	last := make([]string, 0, lastLinesKept)
	for line := range lines {
		if onLine != nil {
			onLine(line)
		}
		last = append(last, line)
		if len(last) > lastLinesKept {
			last = last[len(last)-lastLinesKept:]
		}
	}

	waitErr := cmd.Wait()
	elapsed := time.Since(start)

	result := StepResult{LastLines: last, Elapsed: elapsed}
	var exitErr *exec.ExitError
	switch {
	case waitErr == nil:
		result.OK = true
		result.ExitCode = 0
	case errors.As(waitErr, &exitErr):
		result.ExitCode = exitErr.ExitCode()
	default:
		// Killed by the timeout, or failed to even run.
		result.ExitCode = -1
	}
	return result
}
