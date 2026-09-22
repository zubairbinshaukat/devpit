package elevate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// serveConn is the transport-agnostic core of the worker loop: announce
// hello, then read requests and run them one at a time until shutdown or the
// connection breaks. [Serve] (Windows-only) dials the real named pipe and
// calls this; tests call it directly over [net.Pipe] so the protocol can be
// checked without any OS pipe at all.
func serveConn(ctx context.Context, rwc io.ReadWriteCloser, exec Executor, elevated bool, pid int) error {
	lw := newLineWriter(rwc)
	lr := newLineReader(rwc)

	if err := lw.writeJSON(Event{Event: EventHello, PID: pid, Elevated: elevated}); err != nil {
		return fmt.Errorf("elevate: sending hello: %w", err)
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
		case KindExec:
			handleExec(ctx, lw, exec, req)
		case KindRemove:
			handleRemove(ctx, lw, exec, req)
		default:
			_ = lw.writeJSON(Event{
				ID: req.ID, Event: EventDone, ExitCode: -1,
				Err: fmt.Sprintf("refused: unknown job kind %q", req.Kind),
			})
		}
	}
}

func handleExec(ctx context.Context, lw *lineWriter, exec Executor, req Request) {
	if len(req.Argv) == 0 {
		_ = lw.writeJSON(Event{ID: req.ID, Event: EventDone, ExitCode: -1, Err: "refused: exec requires argv"})
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
	if err != nil {
		done.Err = err.Error()
	}
	_ = lw.writeJSON(done)
}

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
