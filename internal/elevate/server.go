package elevate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// serverLimits are the worker loop's bounds. [serveConn] uses
// [defaultLimits]; tests shrink them so a stuck job can be shown not to
// hold the worker without the test taking minutes.
type serverLimits struct {
	// shutdownWait bounds how long the loop waits, on the way out, for the
	// jobs it cancelled to wind down, and then again for the share lane to
	// be free for the automatic cleanup. An executor that ignores its
	// context must not leave an elevated process alive forever, so after
	// this the loop returns anyway and the process exit ends what is left.
	shutdownWait time.Duration
	// shareOpTimeout bounds one share operation, from the moment it is read
	// to its done event, including any wait for the one before it.
	shareOpTimeout time.Duration
	// maxJobs caps the jobs accepted and not yet finished, so a flood of
	// requests cannot pile up goroutines in an elevated process.
	maxJobs int
}

// defaultLimits are the production bounds.
func defaultLimits() serverLimits {
	return serverLimits{shutdownWait: 15 * time.Second, shareOpTimeout: 3 * time.Minute, maxJobs: 64}
}

// jobTable is the set of jobs the worker has accepted and not yet finished,
// keyed by request ID. It is what makes a cancel safe: [KindCancel] can only
// reach a context that is in this table, and only the worker's own read loop
// puts one there.
type jobTable struct {
	mu   sync.Mutex
	jobs map[string]context.CancelFunc
}

// newJobTable returns an empty table.
func newJobTable() *jobTable {
	return &jobTable{jobs: make(map[string]context.CancelFunc)}
}

// errDuplicateJob and errTooManyJobs are why [jobTable.add] refuses a job.
var (
	errDuplicateJob = errors.New("already running")
	errTooManyJobs  = errors.New("too many jobs in flight")
)

// add records a job that is about to run. It refuses an id already in the
// table, so two requests can never share one cancel handle, and a job past
// the table's limit.
func (t *jobTable) add(id string, cancel context.CancelFunc, limit int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, dup := t.jobs[id]; dup {
		return errDuplicateJob
	}
	if limit > 0 && len(t.jobs) >= limit {
		return errTooManyJobs
	}
	t.jobs[id] = cancel
	return nil
}

// remove forgets a finished job.
func (t *jobTable) remove(id string) {
	t.mu.Lock()
	delete(t.jobs, id)
	t.mu.Unlock()
}

// cancel stops the job named id and reports whether there was one to stop.
func (t *jobTable) cancel(id string) bool {
	t.mu.Lock()
	cancel, ok := t.jobs[id]
	t.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// cancelAll stops every job in the table.
func (t *jobTable) cancelAll() {
	t.mu.Lock()
	all := make([]context.CancelFunc, 0, len(t.jobs))
	for _, cancel := range t.jobs {
		all = append(all, cancel)
	}
	t.mu.Unlock()
	for _, cancel := range all {
		cancel()
	}
}

// serveConn is the transport-agnostic core of the worker loop: announce
// hello, then read requests until shutdown or the connection breaks. [Serve]
// (Windows-only) dials the real named pipe and calls this; tests call it
// directly over [net.Pipe] so the protocol can be checked without any OS
// pipe at all.
func serveConn(ctx context.Context, rwc io.ReadWriteCloser, exec Executor, elevated bool, pid int) error {
	return serveConnWith(ctx, rwc, exec, elevated, pid, defaultLimits())
}

// serveConnWith is [serveConn] with explicit limits.
//
// The loop only reads. Every job (exec, remove, share) runs on its own
// goroutine with its own context in the [jobTable], so a [KindCancel] or a
// shutdown is read and acted on while a long install or a stuck PowerShell
// is still going. Exec and remove jobs run one at a time on one lane; share
// operations run one at a time on the share session's own lane. A job is put
// in the table the moment it is read, before it waits its turn, so a cancel
// that arrives right behind it can never be lost to a race.
//
// On the way out every job is cancelled and waited for (bounded), and only
// then does the share session undo what it set up, so the cleanup never
// races an operation that is still changing the same state.
func serveConnWith(ctx context.Context, rwc io.ReadWriteCloser, exec Executor, elevated bool, pid int, lim serverLimits) error {
	lw := newLineWriter(rwc)
	lr := newLineReader(rwc)

	if err := lw.writeJSON(Event{Event: EventHello, PID: pid, Elevated: elevated}); err != nil {
		return fmt.Errorf("elevate: sending hello: %w", err)
	}

	jobs := newJobTable()
	var wg sync.WaitGroup
	var turn sync.Mutex
	ss := newShareSession(exec)
	defer func() {
		jobs.cancelAll()
		waitOrTimeout(&wg, lim.shutdownWait)
		ss.autoTeardown(lim.shutdownWait)
	}()

	refuse := func(id, why string) {
		_ = lw.writeJSON(Event{ID: id, Event: EventDone, ExitCode: -1, Err: "refused: " + why})
	}
	// start admits one job and runs it on its own goroutine. run returns
	// the job's done event; the job leaves the table before that event is
	// written, so once the TUI has seen a job end, a cancel naming it is
	// always refused rather than silently landing on a finished job.
	start := func(req Request, timeout time.Duration, run func(context.Context) Event) {
		if req.ID == "" {
			refuse("", fmt.Sprintf("a %s job needs an id", req.Kind))
			return
		}
		var jobCtx context.Context
		var cancel context.CancelFunc
		if timeout > 0 {
			jobCtx, cancel = context.WithTimeout(ctx, timeout)
		} else {
			jobCtx, cancel = context.WithCancel(ctx)
		}
		if err := jobs.add(req.ID, cancel, lim.maxJobs); err != nil {
			cancel()
			if errors.Is(err, errDuplicateJob) {
				refuse(req.ID, fmt.Sprintf("a job with id %q is already running", req.ID))
			} else {
				refuse(req.ID, err.Error())
			}
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			done := run(jobCtx)
			jobs.remove(req.ID)
			cancel()
			_ = lw.writeJSON(done)
		}()
	}

	for {
		var req Request
		if err := lr.readJSON(&req); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}

		switch req.Kind {
		case KindShutdown:
			return nil
		case KindExec, KindRemove:
			start(req, 0, func(jobCtx context.Context) Event {
				turn.Lock()
				defer turn.Unlock()
				if req.Kind == KindExec {
					return handleExec(jobCtx, lw, exec, req)
				}
				return handleRemove(jobCtx, exec, req)
			})
		case KindShare:
			start(req, lim.shareOpTimeout, func(jobCtx context.Context) Event {
				return handleShare(jobCtx, lw, ss, req)
			})
		case KindCancel:
			handleCancel(lw, jobs, req)
		default:
			refuse(req.ID, fmt.Sprintf("unknown job kind %q", req.Kind))
		}
	}
}

// waitOrTimeout waits for wg, giving up after d. It reports whether wg
// finished.
func waitOrTimeout(wg *sync.WaitGroup, d time.Duration) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-done:
		return true
	case <-t.C:
		return false
	}
}

// handleCancel stops the one job req.Target names. It says nothing when it
// does: the job's own "done" event is the answer. A target that is empty or
// not a job in flight is refused with a reason on the cancel's own ID, so it
// cannot be mistaken for the end of some other job.
func handleCancel(lw *lineWriter, jobs *jobTable, req Request) {
	if req.Target != "" && jobs.cancel(req.Target) {
		return
	}
	_ = lw.writeJSON(Event{
		ID: req.ID, Event: EventDone, ExitCode: -1,
		Err: fmt.Sprintf("refused: no running job with id %q", req.Target),
	})
}

// handleExec runs one exec job under ctx, which is cancelled by a
// [KindCancel] for it or by shutdown, and adds the job's own timeout on top.
// The timeout starts here, when the job gets its turn, not when it was read.
// It streams the job's lines and returns its done event.
func handleExec(ctx context.Context, lw *lineWriter, exec Executor, req Request) Event {
	if len(req.Argv) == 0 {
		return Event{ID: req.ID, Event: EventDone, ExitCode: -1, Err: "refused: exec requires argv"}
	}
	if ctx.Err() != nil {
		// Cancelled while it waited its turn: never start it.
		return Event{ID: req.ID, Event: EventDone, ExitCode: -1, Err: "cancelled"}
	}

	runCtx := ctx
	if req.TimeoutSec > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutSec)*time.Second)
		defer cancel()
	}

	onLine := func(stream, text string) {
		_ = lw.writeJSON(Event{ID: req.ID, Event: EventLine, Stream: stream, Text: text})
	}
	code, err := exec.Exec(runCtx, req.Argv, onLine)

	// A stopped job is never a success, whatever exit code the stop left:
	// a process ended through its job object can report 0.
	done := Event{ID: req.ID, Event: EventDone, ExitCode: code}
	switch {
	case ctx.Err() != nil:
		done.ExitCode, done.Err = -1, "cancelled"
	case runCtx.Err() != nil:
		done.ExitCode, done.Err = -1, fmt.Sprintf("timed out after %ds", req.TimeoutSec)
	case err != nil:
		done.Err = err.Error()
		if done.ExitCode == 0 {
			done.ExitCode = -1
		}
	}
	return done
}

// handleRemove runs one remove job, after the whitelist has said its path
// may be touched, and returns its done event.
func handleRemove(ctx context.Context, exec Executor, req Request) Event {
	if err := validateRemovePath(req.Path); err != nil {
		return Event{ID: req.ID, Event: EventDone, ExitCode: -1, Err: err.Error()}
	}
	if ctx.Err() != nil {
		return Event{ID: req.ID, Event: EventDone, ExitCode: -1, Err: "cancelled"}
	}

	done := Event{ID: req.ID, Event: EventDone}
	if err := exec.Remove(ctx, req.Path); err != nil {
		done.ExitCode = -1
		done.Err = err.Error()
	}
	return done
}
