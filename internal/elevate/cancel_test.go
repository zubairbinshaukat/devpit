package elevate

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// gateExecutor is an [Executor] whose "block" command runs until its context
// is cancelled and whose every other command returns at once, so a test can
// decide exactly which job is in flight when it sends a cancel.
type gateExecutor struct {
	mu        sync.Mutex
	started   []string
	cancelled []string
	running   chan string
}

func newGateExecutor() *gateExecutor { return &gateExecutor{running: make(chan string, 8)} }

func (g *gateExecutor) Exec(ctx context.Context, argv []string, onLine func(stream, text string)) (int, error) {
	g.mu.Lock()
	g.started = append(g.started, argv[0])
	g.mu.Unlock()
	if argv[0] != "block" {
		onLine("stdout", "ran "+argv[0])
		return 0, nil
	}
	g.running <- argv[1]
	<-ctx.Done()
	g.mu.Lock()
	g.cancelled = append(g.cancelled, argv[1])
	g.mu.Unlock()
	// A killed process reports an exit code, not an error, as the real
	// executor does; the server must still call the job cancelled.
	return 1, nil
}

func (*gateExecutor) Remove(context.Context, string) error { return nil }

func (g *gateExecutor) startedNames() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.started...)
}

func (g *gateExecutor) cancelledNames() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.cancelled...)
}

// waitRunning blocks until a "block" job with the given tag is in flight.
func (g *gateExecutor) waitRunning(t *testing.T, tag string) {
	t.Helper()
	select {
	case got := <-g.running:
		if got != tag {
			t.Fatalf("running job = %q, want %q", got, tag)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("job %q never started", tag)
	}
}

// rawPeer is the TUI's end of a pipe driven by hand, so a test can send
// requests the real client never would.
type rawPeer struct {
	lr *lineReader
	lw *lineWriter
}

// dialRaw connects to serveConn over net.Pipe and reads hello.
func dialRaw(ctx context.Context, t *testing.T, exec Executor) (*rawPeer, <-chan error) {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	serveErr := make(chan error, 1)
	go func() { serveErr <- serveConn(ctx, serverConn, exec, true, 1) }()
	t.Cleanup(func() { _ = clientConn.Close() })
	p := &rawPeer{lr: newLineReader(clientConn), lw: newLineWriter(clientConn)}
	var hello Event
	if err := p.lr.readJSON(&hello); err != nil || hello.Event != EventHello {
		t.Fatalf("hello = %+v, err = %v", hello, err)
	}
	return p, serveErr
}

// next reads the next event, failing the test if none comes.
func (p *rawPeer) next(t *testing.T) Event {
	t.Helper()
	type res struct {
		ev  Event
		err error
	}
	ch := make(chan res, 1)
	go func() {
		var ev Event
		err := p.lr.readJSON(&ev)
		ch <- res{ev, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("reading an event: %v", r.err)
		}
		return r.ev
	case <-time.After(3 * time.Second):
		t.Fatal("no event arrived")
		return Event{}
	}
}

func (p *rawPeer) send(t *testing.T, req Request) {
	t.Helper()
	if err := p.lw.writeJSON(req); err != nil {
		t.Fatalf("send %+v: %v", req, err)
	}
}

// Cancelling one command's context stops that command in the worker and
// leaves the worker ready for the next one.
func TestCancellingAnExecStopsOnlyThatCommand(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ge := newGateExecutor()
	client, _ := dial(ctx, t, ge)

	jobCtx, stopJob := context.WithCancel(ctx)
	execDone := make(chan error, 1)
	go func() {
		_, err := client.Exec(jobCtx, []string{"block", "one"}, 0, nil)
		execDone <- err
	}()
	ge.waitRunning(t, "one")
	stopJob()

	select {
	case err := <-execDone:
		if err == nil || !strings.Contains(err.Error(), "canceled") {
			t.Fatalf("Exec error = %v, want the context's cancellation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Exec did not return after its context was cancelled")
	}
	if got := ge.cancelledNames(); len(got) != 1 || got[0] != "one" {
		t.Fatalf("worker cancelled %v, want [one]", got)
	}

	// The worker is idle and healthy: the next command runs normally.
	code, err := client.Exec(ctx, []string{"echo"}, 0, nil)
	if err != nil || code != 0 {
		t.Fatalf("next Exec = %d, %v; want a clean run", code, err)
	}
}

// A cancel names a job id and nothing else. One that names no job in flight
// (unknown, empty, or already finished) is refused and kills nothing.
func TestCancelRefusesAnythingThatIsNotARunningJob(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ge := newGateExecutor()
	peer, _ := dialRaw(ctx, t, ge)

	peer.send(t, Request{ID: "run", Kind: KindExec, Argv: []string{"block", "keep"}})
	ge.waitRunning(t, "keep")

	for _, target := range []string{"", "nope", "run2"} {
		peer.send(t, Request{ID: "c-" + target, Kind: KindCancel, Target: target})
		ev := peer.next(t)
		if ev.Event != EventDone || ev.ID != "c-"+target || !strings.Contains(ev.Err, "refused") {
			t.Fatalf("cancel of %q: got %+v, want a refusal on the cancel's own id", target, ev)
		}
	}
	if got := ge.cancelledNames(); len(got) != 0 {
		t.Fatalf("a refused cancel still stopped %v", got)
	}

	// The job that is running can be cancelled by its own id.
	peer.send(t, Request{ID: "c-run", Kind: KindCancel, Target: "run"})
	ev := peer.next(t)
	if ev.Event != EventDone || ev.ID != "run" || ev.Err != "cancelled" {
		t.Fatalf("done = %+v, want the running job reported cancelled", ev)
	}

	// Once finished, its id is no longer a target.
	peer.send(t, Request{ID: "late", Kind: KindCancel, Target: "run"})
	if ev := peer.next(t); ev.ID != "late" || !strings.Contains(ev.Err, "refused") {
		t.Fatalf("late cancel: got %+v, want a refusal", ev)
	}
}

// A cancel for a job that is still waiting its turn ends that job without
// ever starting it, and does not touch the job that is running.
func TestCancelOfAQueuedJobNeverStartsItAndSparesTheRunningOne(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ge := newGateExecutor()
	peer, _ := dialRaw(ctx, t, ge)

	peer.send(t, Request{ID: "first", Kind: KindExec, Argv: []string{"block", "first"}})
	ge.waitRunning(t, "first")
	peer.send(t, Request{ID: "second", Kind: KindExec, Argv: []string{"queued"}})
	peer.send(t, Request{ID: "c", Kind: KindCancel, Target: "second"})

	// The queued job is only released once the running one ends.
	peer.send(t, Request{ID: "c2", Kind: KindCancel, Target: "first"})
	got := map[string]Event{}
	for len(got) < 2 {
		ev := peer.next(t)
		if ev.Event == EventDone {
			got[ev.ID] = ev
		}
	}
	if got["second"].Err != "cancelled" {
		t.Errorf("queued job done = %+v, want cancelled", got["second"])
	}
	if got["first"].Err != "cancelled" {
		t.Errorf("running job done = %+v, want cancelled", got["first"])
	}
	for _, name := range ge.startedNames() {
		if name == "queued" {
			t.Error("a job cancelled while queued was started anyway")
		}
	}
}

// A second job cannot reuse the id of one in flight, or one cancel could
// reach two jobs.
func TestDuplicateJobIDIsRefused(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ge := newGateExecutor()
	peer, _ := dialRaw(ctx, t, ge)
	peer.send(t, Request{ID: "same", Kind: KindExec, Argv: []string{"block", "a"}})
	ge.waitRunning(t, "a")
	peer.send(t, Request{ID: "same", Kind: KindExec, Argv: []string{"echo"}})
	ev := peer.next(t)
	if ev.ID != "same" || !strings.Contains(ev.Err, "already running") {
		t.Fatalf("got %+v, want a refusal of the duplicate id", ev)
	}
}

// Shutdown ends the loop and cancels whatever is still running, so closing
// the client can never leave an installer running behind an idle worker.
func TestShutdownCancelsRunningJobs(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ge := newGateExecutor()
	peer, serveErr := dialRaw(ctx, t, ge)
	peer.send(t, Request{ID: "r", Kind: KindExec, Argv: []string{"block", "r"}})
	ge.waitRunning(t, "r")
	peer.send(t, Request{Kind: KindShutdown})
	// net.Pipe is unbuffered: read the cancelled job's last word so the
	// worker's write is not left waiting on a reader.
	if ev := peer.next(t); ev.ID != "r" || ev.Err != "cancelled" {
		t.Fatalf("done = %+v, want job r reported cancelled", ev)
	}

	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("serveConn = %v, want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serveConn did not return after shutdown")
	}
	if got := ge.cancelledNames(); len(got) != 1 || got[0] != "r" {
		t.Fatalf("shutdown cancelled %v, want [r]", got)
	}
}
