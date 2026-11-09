package elevate

import (
	"context"
	"errors"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/sysinfo"
)

// KindSpecs asks the worker for the drives' wear and error counters, the one
// part of Settings › PC specs that needs admin. The request carries nothing
// but its ID: no command, no path, no script. The worker runs only the read
// compiled into internal/sysinfo, and anything that arrives with an argument
// is refused without running anything.
const KindSpecs Kind = "specs"

// specsTimeout bounds the worker's read, from the moment it is read.
const specsTimeout = sysinfo.ReliabilityLimit

// reliabilityReader is the optional [Executor] method a test uses to stand
// in for the real read. [DefaultExecutor] does not have it, so production
// always runs [sysinfo.ReadReliability].
type reliabilityReader interface {
	ReadReliability(ctx context.Context) ([]sysinfo.Reliability, error)
}

// validateSpecs refuses a specs request that carries anything at all.
func validateSpecs(req Request) error {
	if len(req.Argv) != 0 || req.Path != "" || req.Share != nil || req.Target != "" || req.TimeoutSec != 0 {
		return errors.New("refused: a specs request takes no arguments")
	}
	return nil
}

// handleSpecs runs the fixed read and sends one line of JSON, then done.
func handleSpecs(ctx context.Context, lw *lineWriter, exec Executor, req Request) Event {
	if err := validateSpecs(req); err != nil {
		return Event{ID: req.ID, Event: EventDone, ExitCode: -1, Err: err.Error()}
	}
	read := sysinfo.ReadReliability
	if r, ok := exec.(reliabilityReader); ok {
		read = r.ReadReliability
	}
	rs, err := read(ctx)
	if err == nil {
		var text string
		if text, err = sysinfo.EncodeReliability(rs); err == nil {
			_ = lw.writeJSON(Event{ID: req.ID, Event: EventLine, Stream: "specs", Text: text})
		}
	}
	done := Event{ID: req.ID, Event: EventDone}
	switch {
	case ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded):
		done.ExitCode, done.Err = -1, "the drive read timed out"
	case ctx.Err() != nil:
		done.ExitCode, done.Err = -1, "cancelled"
	case err != nil:
		done.ExitCode, done.Err = -1, err.Error()
	}
	return done
}

// Specs asks the worker for the drives' counters and waits for them. It
// stops the read in the worker when ctx ends.
func (c *Client) Specs(ctx context.Context) ([]sysinfo.Reliability, error) {
	id := newID()
	ch := c.register(id)
	defer c.unregister(id)
	if err := c.send(Request{ID: id, Kind: KindSpecs}); err != nil {
		return nil, err
	}
	var got []sysinfo.Reliability
	var decodeErr error
	limit := time.NewTimer(specsTimeout + 5*time.Second)
	defer limit.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return nil, c.deathError()
			}
			switch ev.Event {
			case EventLine:
				if ev.Stream == "specs" {
					got, decodeErr = sysinfo.DecodeReliability(ev.Text)
				}
			case EventDone:
				if ev.Err != "" {
					return nil, errors.New(ev.Err)
				}
				if decodeErr != nil {
					return nil, decodeErr
				}
				return got, nil
			}
		case <-ctx.Done():
			c.cancelJob(id, ch)
			return nil, ctx.Err()
		case <-limit.C:
			c.cancelJob(id, ch)
			return nil, errors.New("the worker did not answer in time")
		}
	}
}
