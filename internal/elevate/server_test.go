package elevate

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeExecutor is an [Executor] a test can inspect. Exec always emits two
// lines and succeeds; Remove always succeeds. Neither touches the real
// filesystem or spawns a process, which is the point: these tests check the
// protocol, not DefaultExecutor.
type fakeExecutor struct {
	mu          sync.Mutex
	execCalls   [][]string
	removeCalls []string
}

func (f *fakeExecutor) Exec(_ context.Context, argv []string, onLine func(stream, text string)) (int, error) {
	f.mu.Lock()
	f.execCalls = append(f.execCalls, argv)
	f.mu.Unlock()
	onLine("stdout", "line one")
	onLine("stdout", "line two")
	return 0, nil
}

func (f *fakeExecutor) Remove(_ context.Context, path string) error {
	f.mu.Lock()
	f.removeCalls = append(f.removeCalls, path)
	f.mu.Unlock()
	return nil
}

func (f *fakeExecutor) removed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.removeCalls...)
}

// dial builds a connected client/worker pair over net.Pipe, so the whole
// protocol can be exercised without any real OS pipe. It returns once the
// client's hello has arrived.
func dial(ctx context.Context, t *testing.T, exec Executor) (*Client, <-chan error) {
	t.Helper()
	serverConn, clientConn := net.Pipe()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- serveConn(ctx, serverConn, exec, true, 4242)
	}()

	client, err := newClient(ctx, clientConn)
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	return client, serveErr
}

func TestServeFullRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	fe := &fakeExecutor{}
	client, serveErr := dial(ctx, t, fe)

	if client.PID() != 4242 || !client.Elevated() {
		t.Fatalf("hello not applied: pid=%d elevated=%v", client.PID(), client.Elevated())
	}

	var lines []string
	code, err := client.Exec(ctx, []string{"choco", "upgrade", "all", "-y"}, time.Minute, func(stream, text string) {
		lines = append(lines, stream+":"+text)
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	want := []string{"stdout:line one", "stdout:line two"}
	if len(lines) != len(want) || lines[0] != want[0] || lines[1] != want[1] {
		t.Fatalf("lines = %v, want %v", lines, want)
	}

	t.Setenv("WINDIR", `C:\Windows`)
	target := filepath.Join(`C:\Windows`, "Temp", "leftover")
	if err := client.Remove(ctx, target, nil); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got := fe.removed(); len(got) != 1 || got[0] != target {
		t.Fatalf("executor saw removeCalls = %v", got)
	}

	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("serveConn returned %v after shutdown, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serveConn did not return after shutdown")
	}
}

func TestServeRefusesUnknownKind(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	serverConn, clientConn := net.Pipe()
	go func() { _ = serveConn(ctx, serverConn, &fakeExecutor{}, true, 1) }()

	lr := newLineReader(clientConn)
	var hello Event
	if err := lr.readJSON(&hello); err != nil || hello.Event != EventHello {
		t.Fatalf("hello = %+v, err = %v", hello, err)
	}

	lw := newLineWriter(clientConn)
	if err := lw.writeJSON(Request{ID: "z1", Kind: "reformat-disk"}); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}

	var done Event
	if err := lr.readJSON(&done); err != nil {
		t.Fatalf("readJSON: %v", err)
	}
	if done.Event != EventDone || done.ID != "z1" || done.Err == "" {
		t.Fatalf("done = %+v, want a refusal for the unknown kind", done)
	}
	if !strings.Contains(done.Err, "unknown job kind") {
		t.Fatalf("done.Err = %q, want it to name the unknown kind", done.Err)
	}
}

func TestServeRemoveWhitelist(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	t.Setenv("WINDIR", `C:\Windows`)
	t.Setenv("LOCALAPPDATA", "")
	t.Setenv("PROGRAMDATA", "")

	fe := &fakeExecutor{}
	client, _ := dial(ctx, t, fe)

	if err := client.Remove(ctx, `C:\Users\dev\Documents\important.docx`, nil); err == nil {
		t.Fatal("Remove outside the whitelist succeeded, want a refusal")
	} else if !strings.Contains(err.Error(), "refused") {
		t.Fatalf("Remove error = %q, want a refusal reason", err.Error())
	}
	if got := fe.removed(); len(got) != 0 {
		t.Fatalf("executor.Remove was called for a refused path: %v", got)
	}

	allowed := filepath.Join(`C:\Windows`, "Temp", "leftover.tmp")
	if err := client.Remove(ctx, allowed, nil); err != nil {
		t.Fatalf("Remove inside the whitelist failed: %v", err)
	}
	if got := fe.removed(); len(got) != 1 || got[0] != allowed {
		t.Fatalf("executor saw removeCalls = %v, want [%s]", got, allowed)
	}
}

// blockingExecutor emits one line and then hangs until its context is
// cancelled, so a test can close the connection "mid-job" and observe the
// client's reaction.
type blockingExecutor struct{}

func (blockingExecutor) Exec(ctx context.Context, _ []string, onLine func(stream, text string)) (int, error) {
	onLine("stdout", "working")
	<-ctx.Done()
	return -1, ctx.Err()
}

func (blockingExecutor) Remove(context.Context, string) error { return nil }

func TestClientSurfacesWorkerDied(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverConn, clientConn := net.Pipe()
	serveErr := make(chan error, 1)
	go func() { serveErr <- serveConn(ctx, serverConn, blockingExecutor{}, true, 7) }()

	client, err := newClient(ctx, clientConn)
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}

	lineArrived := make(chan struct{})
	execErr := make(chan error, 1)
	go func() {
		_, err := client.Exec(ctx, []string{"long-running"}, 0, func(_, _ string) {
			select {
			case lineArrived <- struct{}{}:
			default:
			}
		})
		execErr <- err
	}()

	select {
	case <-lineArrived:
	case <-time.After(2 * time.Second):
		t.Fatal("never saw the worker's first line")
	}

	// Simulate the worker process dying mid-job: its end of the pipe goes
	// away without a "done" event ever arriving.
	if err := serverConn.Close(); err != nil {
		t.Fatalf("closing the worker's end of the pipe: %v", err)
	}

	select {
	case err := <-execErr:
		var died *WorkerDiedError
		if !errors.As(err, &died) {
			t.Fatalf("Exec returned %v (%T), want a *WorkerDiedError", err, err)
		}
		if len(died.LastLines) == 0 || !strings.Contains(died.LastLines[0], "working") {
			t.Fatalf("died.LastLines = %v, want it to contain the line the worker streamed before dying", died.LastLines)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Exec never returned after the worker died")
	}

	// Release the worker's blocked Exec call and drain its goroutine so it
	// does not leak past the test.
	cancel()
	select {
	case <-serveErr:
	case <-time.After(2 * time.Second):
		t.Fatal("serveConn goroutine leaked past the test")
	}
}
