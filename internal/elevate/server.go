package elevate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// shutdownWait bounds how long serveConn waits, on the way out, for jobs it
// cancelled to wind down. An executor that ignores its context must not
// leave an elevated process alive forever, so after this the loop returns
// anyway and the process exit ends whatever is left.
const shutdownWait = 15 * time.Second

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

// add records a job that is about to run. It reports false when id is
// already in the table, so two requests can never share one cancel handle.
func (t *jobTable) add(id string, cancel context.CancelFunc) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, dup := t.jobs[id]; dup {
		return false
	}
	t.jobs[id] = cancel
	return true
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
//
// The loop only reads. Each exec or remove job runs on its own goroutine,
// one at a time (they queue on a mutex, as the worker has always run one job
// at a time), so a [KindCancel] or a shutdown is read and acted on while a
// long install is still going. A job is put in the [jobTable] the moment it
// is read, before it waits its turn, so a cancel that arrives right behind
// its exec can never be lost to a race.
func serveConn(ctx context.Context, rwc io.ReadWriteCloser, exec Executor, elevated bool, pid int) error {
	lw := newLineWriter(rwc)
	lr := newLineReader(rwc)

	if err := lw.writeJSON(Event{Event: EventHello, PID: pid, Elevated: elevated}); err != nil {
		return fmt.Errorf("elevate: sending hello: %w", err)
	}

	jobs := newJobTable()
	var wg sync.WaitGroup
	var turn sync.Mutex
	defer func() {
		jobs.cancelAll()
		waitOrTimeout(&wg, shutdownWait)
	}()
	ss := newShareSession(exec)
	defer ss.autoTeardown()

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
			jobCtx, cancel := context.WithCancel(ctx)
			if req.ID != "" && !jobs.add(req.ID, cancel) {
				cancel()
				_ = lw.writeJSON(Event{
					ID: req.ID, Event: EventDone, ExitCode: -1,
					Err: fmt.Sprintf("refused: a job with id %q is already running", req.ID),
				})
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer cancel()
				defer jobs.remove(req.ID)
				turn.Lock()
				defer turn.Unlock()
				if req.Kind == KindExec {
					handleExec(jobCtx, lw, exec, req)
				} else {
					handleRemove(jobCtx, lw, exec, req)
				}
			}()
		case KindCancel:
			handleCancel(lw, jobs, req)
		case KindShare:
			handleShare(ctx, lw, ss, req)
		default:
			_ = lw.writeJSON(Event{
				ID: req.ID, Event: EventDone, ExitCode: -1,
				Err: fmt.Sprintf("refused: unknown job kind %q", req.Kind),
			})
		}
	}
}

// waitOrTimeout waits for wg, giving up after d.
func waitOrTimeout(wg *sync.WaitGroup, d time.Duration) {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
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
func handleExec(ctx context.Context, lw *lineWriter, exec Executor, req Request) {
	if len(req.Argv) == 0 {
		_ = lw.writeJSON(Event{ID: req.ID, Event: EventDone, ExitCode: -1, Err: "refused: exec requires argv"})
		return
	}
	if ctx.Err() != nil {
		// Cancelled while it waited its turn: never start it.
		_ = lw.writeJSON(Event{ID: req.ID, Event: EventDone, ExitCode: -1, Err: "cancelled"})
		return
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

	done := Event{ID: req.ID, Event: EventDone, ExitCode: code}
	switch {
	case ctx.Err() != nil:
		done.ExitCode, done.Err = -1, "cancelled"
	case err != nil:
		done.Err = err.Error()
	}
	_ = lw.writeJSON(done)
}

// handleRemove runs one remove job, after the whitelist has said its path
// may be touched.
func handleRemove(ctx context.Context, lw *lineWriter, exec Executor, req Request) {
	if err := validateRemovePath(req.Path); err != nil {
		_ = lw.writeJSON(Event{ID: req.ID, Event: EventDone, ExitCode: -1, Err: err.Error()})
		return
	}

	done := Event{ID: req.ID, Event: EventDone}
	if err := exec.Remove(ctx, req.Path); err != nil {
		done.ExitCode = -1
		done.Err = err.Error()
	}
	_ = lw.writeJSON(done)
}
