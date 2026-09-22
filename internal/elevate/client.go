package elevate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// lastLinesKept caps how many recent output lines a [Client] remembers, for
// [WorkerDiedError.LastLines] when the worker dies mid-job.
const lastLinesKept = 20

// Client talks to an elevated worker process over the JSON-lines protocol.
// Construct one with [Launch]. Jobs run one at a time because the worker
// itself is sequential; calling Exec or Remove again before the previous
// call returns simply queues behind the worker's own loop.
type Client struct {
	rwc io.ReadWriteCloser
	lw  *lineWriter

	pid      int
	elevated bool

	mu        sync.Mutex
	pending   map[string]chan Event
	lastLines []string
	dead      bool
	deathErr  error

	closeOnce sync.Once
	readDone  chan struct{}
}

// PID is the worker process's ID, from its hello.
func (c *Client) PID() int { return c.pid }

// Elevated reports whether the worker's own token is actually elevated,
// from its hello. It should always be true for a worker Launch started
// (the whole point of the "runas" verb), but Devpit checks rather than
// assumes.
func (c *Client) Elevated() bool { return c.elevated }

// newClient wraps an already-connected transport and waits for the worker's
// hello. ctx bounds only that wait; once hello arrives, the client's
// lifetime is tied to rwc until [Client.Close].
func newClient(ctx context.Context, rwc io.ReadWriteCloser) (*Client, error) {
	lr := newLineReader(rwc)

	type helloResult struct {
		ev  Event
		err error
	}
	helloCh := make(chan helloResult, 1)
	go func() {
		var ev Event
		err := lr.readJSON(&ev)
		helloCh <- helloResult{ev, err}
	}()

	var hello Event
	select {
	case r := <-helloCh:
		if r.err != nil {
			_ = rwc.Close()
			return nil, fmt.Errorf("elevate: waiting for hello: %w", r.err)
		}
		hello = r.ev
	case <-ctx.Done():
		_ = rwc.Close()
		return nil, ctx.Err()
	}
	if hello.Event != EventHello {
		_ = rwc.Close()
		return nil, fmt.Errorf("elevate: expected hello, got %q", hello.Event)
	}

	c := &Client{
		rwc:      rwc,
		lw:       newLineWriter(rwc),
		pid:      hello.PID,
		elevated: hello.Elevated,
		pending:  make(map[string]chan Event),
		readDone: make(chan struct{}),
	}
	go c.readLoop(lr)
	return c, nil
}

// readLoop dispatches every event after hello to whichever Exec/Remove call
// is waiting on its ID, until the connection breaks.
func (c *Client) readLoop(lr *lineReader) {
	defer close(c.readDone)
	for {
		var ev Event
		err := lr.readJSON(&ev)
		if err != nil {
			c.killPending(err)
			return
		}
		switch ev.Event {
		case EventLine:
			c.recordLine(ev.Stream, ev.Text)
			c.dispatch(ev)
		case EventDone:
			c.dispatch(ev)
		default:
			// A newer worker sending a message this client does not know
			// about is not a reason to tear down the connection.
		}
	}
}

func (c *Client) dispatch(ev Event) {
	c.mu.Lock()
	ch := c.pending[ev.ID]
	c.mu.Unlock()
	if ch != nil {
		ch <- ev
	}
}

func (c *Client) recordLine(stream, text string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	line := text
	if stream != "" {
		line = stream + ": " + text
	}
	c.lastLines = append(c.lastLines, line)
	if len(c.lastLines) > lastLinesKept {
		c.lastLines = c.lastLines[len(c.lastLines)-lastLinesKept:]
	}
}

// killPending marks the client dead and closes every channel a pending
// Exec/Remove call is blocked on, so they wake up and report
// [WorkerDiedError] instead of hanging forever.
func (c *Client) killPending(err error) {
	if errors.Is(err, io.EOF) {
		err = nil
	}
	c.mu.Lock()
	c.dead = true
	c.deathErr = err
	pending := c.pending
	c.pending = make(map[string]chan Event)
	c.mu.Unlock()

	for _, ch := range pending {
		close(ch)
	}
}

func (c *Client) deathError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	lines := append([]string(nil), c.lastLines...)
	return &WorkerDiedError{LastLines: lines, Err: c.deathErr}
}

// register allocates the reply channel for a new request ID. If the client
// is already dead it returns a closed channel so the caller's select falls
// straight through to the death path.
func (c *Client) register(id string) chan Event {
	ch := make(chan Event, 8)
	c.mu.Lock()
	if c.dead {
		c.mu.Unlock()
		close(ch)
		return ch
	}
	c.pending[id] = ch
	c.mu.Unlock()
	return ch
}

func (c *Client) unregister(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Client) send(req Request) error {
	return c.lw.writeJSON(req)
}

// Exec asks the worker to run argv, blocking until it finishes. onLine, if
// non-nil, is called for every line of stdout/stderr the worker streams
// back, in arrival order. A non-positive timeout means no worker-side
// timeout beyond ctx.
func (c *Client) Exec(ctx context.Context, argv []string, timeout time.Duration, onLine func(stream, text string)) (int, error) {
	if len(argv) == 0 {
		return 0, errors.New("elevate: exec requires at least one argument")
	}

	id := newID()
	ch := c.register(id)
	defer c.unregister(id)

	req := Request{ID: id, Kind: KindExec, Argv: argv}
	if timeout > 0 {
		req.TimeoutSec = int(timeout.Seconds())
	}
	if err := c.send(req); err != nil {
		return 0, err
	}

	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return 0, c.deathError()
			}
			switch ev.Event {
			case EventLine:
				if onLine != nil {
					onLine(ev.Stream, ev.Text)
				}
			case EventDone:
				if ev.Err != "" {
					return ev.ExitCode, errors.New(ev.Err)
				}
				return ev.ExitCode, nil
			}
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
}

// Remove asks the worker to delete path, blocking until it finishes. onLine,
// if non-nil, is called for any progress line the worker streams back. The
// worker refuses anything outside its cleanup-root whitelist; that refusal
// comes back as a plain error from Remove, not a panic or a silent no-op.
func (c *Client) Remove(ctx context.Context, path string, onLine func(text string)) error {
	id := newID()
	ch := c.register(id)
	defer c.unregister(id)

	if err := c.send(Request{ID: id, Kind: KindRemove, Path: path}); err != nil {
		return err
	}

	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return c.deathError()
			}
			switch ev.Event {
			case EventLine:
				if onLine != nil {
					onLine(ev.Text)
				}
			case EventDone:
				if ev.Err != "" {
					return errors.New(ev.Err)
				}
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Close asks the worker to shut down and closes the connection. It is safe
// to call more than once; only the first call does anything. It waits for
// the read loop to exit so no goroutine outlives Close.
func (c *Client) Close() error {
	var err error
	c.closeOnce.Do(func() {
		_ = c.send(Request{Kind: KindShutdown})
		err = c.rwc.Close()
		<-c.readDone
	})
	return err
}
