//go:build windows

package elevate

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestRealNamedPipe proves the actual CreateNamedPipe / ConnectNamedPipe /
// CreateFileW plumbing works, without going through ShellExecuteExW's UAC
// prompt: it plays the TUI's half (createServerPipe + waitForConnect) in
// this goroutine and the worker's half ([Serve], which dials with
// CreateFileW) in another, both in-process. Launch itself still needs a
// real elevation to exercise end to end, which is not something a test
// runner can click through, so this is the next best thing and is gated
// behind an env var since it touches a real OS resource.
func TestRealNamedPipe(t *testing.T) {
	if os.Getenv("DEVPIT_ELEVATE_INTEGRATION") != "1" {
		t.Skip("set DEVPIT_ELEVATE_INTEGRATION=1 to run the real named-pipe integration test")
	}

	pipeName := fmt.Sprintf(`\\.\pipe\devpit-test-%d-%s`, os.Getpid(), mustHex(t))

	h, err := createServerPipe(pipeName)
	if err != nil {
		t.Fatalf("createServerPipe: %v", err)
	}
	conn := &pipeConn{h: h}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- Serve(ctx, pipeName, DefaultExecutor{})
	}()

	connectCtx, connectCancel := context.WithTimeout(ctx, 5*time.Second)
	defer connectCancel()
	if connErr := conn.waitForConnect(connectCtx.Done()); connErr != nil {
		t.Fatalf("waitForConnect: %v", connErr)
	}

	client, err := newClient(ctx, conn)
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	if !client.Elevated() {
		// This test does not elevate; it only proves the pipe plumbing.
		t.Logf("worker token elevated = %v (expected false: no UAC prompt was involved)", client.Elevated())
	}

	var lines []string
	code, err := client.Exec(ctx, []string{"cmd.exe", "/c", "echo hello-from-worker"}, 10*time.Second,
		func(_, text string) { lines = append(lines, text) })
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	found := false
	for _, l := range lines {
		if l == "hello-from-worker" {
			found = true
		}
	}
	if !found {
		t.Fatalf("worker output = %v, want a line \"hello-from-worker\"", lines)
	}

	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("Serve returned %v after shutdown, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after shutdown")
	}
}

func mustHex(t *testing.T) string {
	t.Helper()
	s, err := randomHex(4)
	if err != nil {
		t.Fatalf("randomHex: %v", err)
	}
	return s
}
