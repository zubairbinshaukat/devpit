package elevate

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// stuckShare is a shareFake whose one matching PowerShell script hangs: until
// its context ends, or, with ignoreCancel, until release is closed (an
// executor that does not honour cancellation at all).
type stuckShare struct {
	*shareFake
	match        string
	running      chan struct{}
	ignoreCancel bool
	release      chan struct{}
}

func newStuckShare(match string) *stuckShare {
	return &stuckShare{
		shareFake: newShareFake(), match: match,
		running: make(chan struct{}, 1), release: make(chan struct{}),
	}
}

func (s *stuckShare) Exec(ctx context.Context, argv []string, onLine func(stream, text string)) (int, error) {
	if !strings.Contains(decode(argv), s.match) {
		return s.shareFake.Exec(ctx, argv, onLine)
	}
	select {
	case s.running <- struct{}{}:
	default:
	}
	if s.ignoreCancel {
		<-s.release
	} else {
		<-ctx.Done()
	}
	return 1, nil
}

func (s *stuckShare) waitRunning(t *testing.T) {
	t.Helper()
	select {
	case <-s.running:
	case <-time.After(3 * time.Second):
		t.Fatal("the stuck operation never started")
	}
}

// dialRawWith is dialRaw with explicit server limits.
func dialRawWith(ctx context.Context, t *testing.T, exec Executor, lim serverLimits) (*rawPeer, <-chan error) {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	serveErr := make(chan error, 1)
	go func() { serveErr <- serveConnWith(ctx, serverConn, exec, true, 1, lim) }()
	t.Cleanup(func() { _ = clientConn.Close() })
	p := &rawPeer{lr: newLineReader(clientConn), lw: newLineWriter(clientConn)}
	var hello Event
	if err := p.lr.readJSON(&hello); err != nil || hello.Event != EventHello {
		t.Fatalf("hello = %+v, err = %v", hello, err)
	}
	return p, serveErr
}

// doneFor reads events until the done event of id, and returns it.
func (p *rawPeer) doneFor(t *testing.T, id string) Event {
	t.Helper()
	for {
		ev := p.next(t)
		if ev.Event == EventDone && ev.ID == id {
			return ev
		}
	}
}

// infoLine reads one progress line.
func (p *rawPeer) infoLine(t *testing.T) {
	t.Helper()
	if ev := p.next(t); ev.Event != EventLine {
		t.Fatalf("got %+v, want a progress line", ev)
	}
}

func waitServed(t *testing.T, serveErr <-chan error, within time.Duration) {
	t.Helper()
	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("serveConn = %v, want nil", err)
		}
	case <-time.After(within):
		t.Fatalf("serveConn did not return within %v", within)
	}
}

var profileReq = ShareRequest{Op: ShareOpProfile, Index: 7, Category: "Private", Original: "Public"}

// A share operation stuck in PowerShell no longer holds the worker's loop:
// a cancel for it is read and acted on, the worker goes on serving, and the
// step the stuck operation may have made is still undone at the end.
func TestAStuckShareOperationCanBeCancelledAndTheWorkerCarriesOn(t *testing.T) {
	shareEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f := newStuckShare("-NetworkCategory Private")
	peer, serveErr := dialRawWith(ctx, t, f, defaultLimits())

	req := profileReq
	peer.send(t, Request{ID: "p", Kind: KindShare, Share: &req})
	peer.infoLine(t) // net.Pipe is unbuffered: take the progress line
	f.waitRunning(t)
	peer.send(t, Request{ID: "c", Kind: KindCancel, Target: "p"})
	if ev := peer.doneFor(t, "p"); ev.Err != "cancelled" {
		t.Fatalf("done = %+v, want the stuck operation cancelled", ev)
	}

	peer.send(t, Request{ID: "f", Kind: KindShare, Share: &ShareRequest{Op: ShareOpFirewall}})
	if ev := peer.doneFor(t, "f"); ev.Err != "" {
		t.Fatalf("the next operation failed: %+v", ev)
	}

	peer.send(t, Request{Kind: KindShutdown})
	waitServed(t, serveErr, 3*time.Second)
	all := strings.Join(f.commands(), "\n")
	for _, want := range []string{"-NetworkCategory Public", "Enabled False"} {
		if !strings.Contains(all, want) {
			t.Errorf("the worker did not undo %q:\n%s", want, all)
		}
	}
}

// Every share operation has a time limit.
func TestAShareOperationThatNeverEndsTimesOut(t *testing.T) {
	shareEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f := newStuckShare("-NetworkCategory Private")
	lim := defaultLimits()
	lim.shareOpTimeout = 200 * time.Millisecond
	peer, _ := dialRawWith(ctx, t, f, lim)

	req := profileReq
	peer.send(t, Request{ID: "p", Kind: KindShare, Share: &req})
	if ev := peer.doneFor(t, "p"); !strings.Contains(ev.Err, "timed out") {
		t.Fatalf("done = %+v, want a time-out", ev)
	}
}

// A shutdown is acted on at once even while a share operation is stuck, and
// the cleanup still runs once that operation has been cancelled.
func TestShutdownIsNotHeldByAStuckShareOperation(t *testing.T) {
	shareEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f := newStuckShare("-NetworkCategory Private")
	peer, serveErr := dialRawWith(ctx, t, f, defaultLimits())

	req := profileReq
	peer.send(t, Request{ID: "p", Kind: KindShare, Share: &req})
	peer.infoLine(t) // net.Pipe is unbuffered: take the progress line
	f.waitRunning(t)
	peer.send(t, Request{Kind: KindShutdown})
	if ev := peer.doneFor(t, "p"); ev.Err != "cancelled" {
		t.Fatalf("done = %+v, want the operation cancelled by the shutdown", ev)
	}
	waitServed(t, serveErr, 3*time.Second)
	if !strings.Contains(strings.Join(f.commands(), "\n"), "-NetworkCategory Public") {
		t.Error("the cleanup after the shutdown did not restore the network")
	}
}

// An operation that ignores its cancel entirely still cannot keep the
// elevated worker alive: the loop gives up waiting after its bound, and the
// cleanup, which would race that operation, is left to the manifest.
func TestAnOperationThatIgnoresCancelCannotHoldTheWorker(t *testing.T) {
	shareEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f := newStuckShare("-NetworkCategory Private")
	f.ignoreCancel = true
	defer close(f.release)
	lim := defaultLimits()
	lim.shutdownWait = 200 * time.Millisecond
	peer, serveErr := dialRawWith(ctx, t, f, lim)

	req := profileReq
	peer.send(t, Request{ID: "p", Kind: KindShare, Share: &req})
	peer.infoLine(t) // net.Pipe is unbuffered: take the progress line
	f.waitRunning(t)
	start := time.Now()
	peer.send(t, Request{Kind: KindShutdown})
	waitServed(t, serveErr, 3*time.Second)
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("shutdown took %v", d)
	}
	if strings.Contains(strings.Join(f.commands(), "\n"), "-NetworkCategory Public") {
		t.Error("the cleanup ran beside an operation that was still running")
	}
}

// Every job needs an id (it is what a cancel names), and the number of jobs
// in flight is capped.
func TestJobsNeedAnIDAndAreCapped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ge := newGateExecutor()
	lim := defaultLimits()
	lim.maxJobs = 2
	peer, _ := dialRawWith(ctx, t, ge, lim)

	peer.send(t, Request{Kind: KindExec, Argv: []string{"echo"}})
	if ev := peer.next(t); !strings.Contains(ev.Err, "needs an id") {
		t.Fatalf("got %+v, want a refusal of a job without an id", ev)
	}

	peer.send(t, Request{ID: "a", Kind: KindExec, Argv: []string{"block", "a"}})
	ge.waitRunning(t, "a")
	peer.send(t, Request{ID: "b", Kind: KindExec, Argv: []string{"queued"}})
	peer.send(t, Request{ID: "c", Kind: KindExec, Argv: []string{"queued"}})
	if ev := peer.next(t); ev.ID != "c" || !strings.Contains(ev.Err, "too many") {
		t.Fatalf("got %+v, want job c refused as one too many", ev)
	}
	for _, name := range ge.startedNames() {
		if name == "queued" {
			t.Fatal("a job ran before the running one finished")
		}
	}
}

// Cancelling Share's context stops the operation in the worker, so the stop
// the caller sends next runs at once instead of queueing behind it.
func TestCancellingShareStopsTheOperationInTheWorker(t *testing.T) {
	shareEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f := newStuckShare("-NetworkCategory Private")
	client, _ := dial(ctx, t, f)
	defer client.Close() //nolint:errcheck // test cleanup

	opCtx, stopOp := context.WithCancel(ctx)
	shareErr := make(chan error, 1)
	go func() { shareErr <- client.Share(opCtx, profileReq, nil) }()
	f.waitRunning(t)
	stopOp()
	select {
	case err := <-shareErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Share = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Share did not return after its context was cancelled")
	}

	stopCtx, stopCancel := context.WithTimeout(ctx, 3*time.Second)
	defer stopCancel()
	if err := client.Share(stopCtx, ShareRequest{Op: ShareOpStop}, nil); err != nil {
		t.Fatalf("stop after a cancel: %v", err)
	}
	if !strings.Contains(strings.Join(f.commands(), "\n"), "-NetworkCategory Public") {
		t.Error("stop did not restore the network the cancelled step may have switched")
	}
}

// A call that has stopped listening can never wedge the client's read loop:
// dispatch gives up on it the moment it unregisters.
func TestDispatchNeverBlocksOnACallThatHasGone(t *testing.T) {
	c := &Client{pending: make(map[string]*pending)}
	c.register("x")
	for range 8 { // fill the reply buffer; nobody reads it
		c.dispatch(Event{ID: "x", Event: EventLine})
	}
	done := make(chan struct{})
	go func() {
		c.dispatch(Event{ID: "x", Event: EventLine})
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	c.unregister("x")
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch stayed blocked on a call that had unregistered")
	}
	c.unregister("x") // a second unregister is harmless
}
