//go:build windows

package elevate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

const (
	defaultConnectTimeout = 30 * time.Second
	defaultHelloTimeout   = 10 * time.Second
	pipeBufferSize        = 64 * 1024

	// These two flag names must match FlagElevatedWorker and FlagPipe in
	// cmd/devpit/stubs.go exactly; internal/elevate cannot import
	// cmd/devpit (that package imports this one), so the strings are
	// duplicated here on purpose rather than shared.
	workerFlag = "--elevated-worker"
	pipeFlag   = "--pipe"
)

// Launch starts exe as a hidden, elevated worker via ShellExecuteExW's
// "runas" verb — the single UAC prompt — and connects to it over a named
// pipe. It returns once the worker's hello has arrived.
//
// If the user declines the UAC prompt, Launch returns a *[DeclinedError].
func Launch(ctx context.Context, exe string, opts Options) (*Client, error) {
	connectTimeout := opts.ConnectTimeout
	if connectTimeout <= 0 {
		connectTimeout = defaultConnectTimeout
	}
	helloTimeout := opts.HelloTimeout
	if helloTimeout <= 0 {
		helloTimeout = defaultHelloTimeout
	}

	suffix, err := randomHex(4)
	if err != nil {
		return nil, fmt.Errorf("elevate: generating pipe name: %w", err)
	}
	pipeName := fmt.Sprintf(`\\.\pipe\devpit-%d-%s`, os.Getpid(), suffix)

	h, err := createServerPipe(pipeName)
	if err != nil {
		return nil, fmt.Errorf("elevate: creating pipe: %w", err)
	}
	conn := &pipeConn{h: h}

	args := append([]string{workerFlag, pipeFlag, pipeName}, opts.ExtraArgs...)
	if err := shellExecuteRunas(exe, joinArgs(args)); err != nil {
		_ = conn.Close()
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return nil, &DeclinedError{}
		}
		return nil, fmt.Errorf("elevate: launching %s: %w", exe, err)
	}

	connectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := conn.waitForConnect(connectCtx.Done()); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("elevate: waiting for worker to connect: %w", err)
	}

	helloCtx, cancel2 := context.WithTimeout(ctx, helloTimeout)
	defer cancel2()
	return newClient(helloCtx, conn)
}

// createServerPipe creates the TUI-side end of the named pipe: duplex,
// message-boundary-free (byte mode, since the codec already frames on
// newlines), a single instance since exactly one worker ever connects, and
// overlapped so [pipeConn.waitForConnect] can be given a deadline. sa is nil
// for the default security descriptor, which grants the pipe's creator (and
// so the same user's elevated worker) access — exactly what a same-user UAC
// elevation needs, nothing more.
func createServerPipe(name string) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	return windows.CreateNamedPipe(p,
		windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_OVERLAPPED,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT,
		1, pipeBufferSize, pipeBufferSize, 0, nil)
}

// joinArgs builds a Windows command-line parameter string. Devpit's own
// reserved flags and its random pipe names never contain spaces or quotes;
// this still quotes defensively rather than assuming that of ExtraArgs too.
func joinArgs(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " \t\"") {
			parts[i] = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
		} else {
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}
