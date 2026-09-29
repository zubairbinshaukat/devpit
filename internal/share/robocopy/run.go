package robocopy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Runner starts robocopy and waits for it. The real one shells out; tests
// pass a fake that appends to a log and returns an exit code. The console
// output is not read at all: robocopy is told to write a Unicode log file,
// which is the only output that keeps every file name intact.
type Runner interface {
	// Run runs robocopy with args and returns its exit code. A non-nil error
	// means it could not start or was cancelled; a process that ran and
	// exited with any code returns that code and a nil error.
	Run(ctx context.Context, args []string) (int, error)
}

// pollEvery is how often a running copy's log is read.
const pollEvery = 500 * time.Millisecond

// Tool is a robocopy that can be run and whose log can be read.
type Tool struct {
	// Runner runs the process.
	Runner Runner
	// Poll is how often the log is read while the copy runs. Zero means
	// half a second.
	Poll time.Duration
}

// Result is how a copy ended.
type Result struct {
	// Exit is the decoded exit code.
	Exit Exit
	// Summary is the table at the end of the log, when there was one.
	Summary Summary
	// HasSummary reports whether Summary was found.
	HasSummary bool
	// Progress is the final progress, including the files that failed.
	Progress Progress
}

// DryRun lists what copying src to dst would do, using log as the log file.
// It returns the plan, or an error when robocopy could not run at all.
func (t Tool) DryRun(ctx context.Context, src, dst, logPath string, log LogSource) (Plan, error) {
	code, err := t.Runner.Run(ctx, DryRunArgs(src, dst, logPath))
	if err != nil {
		return Plan{}, fmt.Errorf("robocopy dry run: %w", err)
	}
	b, rerr := log.ReadFrom(0)
	if rerr != nil {
		return Plan{}, fmt.Errorf("reading the dry run log: %w", rerr)
	}
	text := DecodeLog(b)
	if e := DecodeExit(code); e.Fatal || e.SomeFailed {
		return ParseDryRun(text, src, dst), &RunError{Exit: e, Log: tailLines(text, 6)}
	}
	return ParseDryRun(text, src, dst), nil
}

// RunError is robocopy failing outright, with the last lines it wrote so the
// error can be looked up by its number.
type RunError struct {
	// Exit is the decoded exit code.
	Exit Exit
	// Log is the tail of the log.
	Log string
}

// Error implements error.
func (e *RunError) Error() string {
	return fmt.Sprintf("robocopy exited with code %d", e.Exit.Code)
}

// tailLines returns the last n non-empty lines of text.
func tailLines(text string, n int) string {
	var keep []string
	for l := range strings.SplitSeq(text, "\n") {
		if strings.TrimSpace(l) != "" {
			keep = append(keep, strings.TrimRight(l, "\r"))
		}
	}
	if len(keep) > n {
		keep = keep[len(keep)-n:]
	}
	return strings.Join(keep, "\n")
}

// Copy runs the real copy. While it runs, the log is read every poll and
// onProgress is called with the running progress after each read that found
// something new. onProgress runs on the caller's goroutine, so it may touch
// the caller's state without a lock.
//
// A cancelled context stops robocopy (the runner kills it) and Copy returns
// the progress so far with the context's error, so a screen can save it for
// Resume.
func (t Tool) Copy(ctx context.Context, src, dst, logPath string, log LogSource, onProgress func(Progress)) (Result, error) {
	poll := t.Poll
	if poll <= 0 {
		poll = pollEvery
	}
	type done struct {
		code int
		err  error
	}
	ch := make(chan done, 1)
	go func() {
		code, err := t.Runner.Run(ctx, CopyArgs(src, dst, logPath))
		ch <- done{code, err}
	}()

	tr := NewTracker()
	tail := NewTail(log)
	drain := func() {
		lines, err := tail.Poll()
		if err != nil || len(lines) == 0 {
			return
		}
		for _, l := range lines {
			tr.Feed(l)
		}
		if onProgress != nil {
			onProgress(tr.Snapshot())
		}
	}

	tick := time.NewTicker(poll)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			drain()
		case d := <-ch:
			drain()
			res := Result{Exit: DecodeExit(d.code), Progress: tr.Snapshot()}
			if b, err := log.ReadFrom(0); err == nil {
				res.Summary, res.HasSummary = ParseSummary(DecodeLog(b))
			}
			if d.err != nil {
				if errors.Is(d.err, context.Canceled) || errors.Is(d.err, context.DeadlineExceeded) {
					return res, d.err
				}
				return res, fmt.Errorf("robocopy: %w", d.err)
			}
			return res, nil
		}
	}
}
