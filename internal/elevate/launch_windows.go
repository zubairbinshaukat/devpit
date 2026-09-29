//go:build windows

package elevate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

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

	// workerPipeSDDL is the pipe's access list. Only SYSTEM and
	// Administrators may open it, which is exactly the elevated worker
	// (same-user UAC and over-the-shoulder elevation both run it with
	// Administrators enabled) and nobody else: not Everyone and anonymous,
	// which the default pipe security gives read access, and not the TUI's
	// own unelevated user, so a program running beside Devpit cannot pose
	// as the worker. OWNER RIGHTS gets read-control only, which takes away
	// the owner's implicit right to rewrite this list.
	workerPipeSDDL = "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;RC;;;OW)"
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

	suffix, err := randomHex(16)
	if err != nil {
		return nil, fmt.Errorf("elevate: generating pipe name: %w", err)
	}
	name := pipeName(os.Getpid(), suffix)

	h, err := createServerPipe(name)
	if err != nil {
		return nil, fmt.Errorf("elevate: creating pipe: %w", err)
	}
	conn, err := newPipeConn(h)
	if err != nil {
		return nil, fmt.Errorf("elevate: creating pipe: %w", err)
	}

	args := append([]string{workerFlag, pipeFlag, name}, opts.ExtraArgs...)
	proc, err := shellExecuteRunas(exe, joinArgs(args))
	if err != nil {
		_ = conn.Close()
		return nil, launchError(exe, err)
	}
	var workerPID uint32
	if proc != 0 {
		workerPID, _ = windows.GetProcessId(proc)
		_ = windows.CloseHandle(proc)
	}

	connectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := conn.waitForConnect(connectCtx.Done()); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("elevate: waiting for worker to connect: %w", err)
	}
	if err := verifyClient(h, workerPID, exe); err != nil {
		_ = conn.Close()
		return nil, err
	}

	helloCtx, cancel2 := context.WithTimeout(ctx, helloTimeout)
	defer cancel2()
	return newClient(helloCtx, conn)
}

// launchError turns ShellExecuteExW's failure into what callers act on: a
// declined UAC prompt (ERROR_CANCELLED, 1223) is a *[DeclinedError], so the
// screens can say "skipped (needs admin)" rather than show an error code.
func launchError(exe string, err error) error {
	if errors.Is(err, windows.ERROR_CANCELLED) {
		return &DeclinedError{}
	}
	return fmt.Errorf("elevate: launching %s: %w", exe, err)
}

// verifyClient refuses a pipe client that is not the worker Launch started.
// The pipe's access list already keeps out everything that is not elevated;
// this also keeps out another elevated program that got there first. When
// ShellExecuteExW gave back the worker's process, its PID must match;
// otherwise the client must at least be running exe.
func verifyClient(h windows.Handle, workerPID uint32, exe string) error {
	var pid uint32
	if err := windows.GetNamedPipeClientProcessId(h, &pid); err != nil {
		return fmt.Errorf("elevate: refused: cannot tell who connected to the pipe: %w", err)
	}
	if workerPID != 0 {
		if pid != workerPID {
			return fmt.Errorf("elevate: refused: process %d connected to the pipe, not the worker (%d)", pid, workerPID)
		}
		return nil
	}
	image, err := processImage(pid)
	if err != nil {
		return fmt.Errorf("elevate: refused: cannot check process %d: %w", pid, err)
	}
	if !samePath(image, exe) {
		return fmt.Errorf("elevate: refused: %s connected to the pipe, not %s", image, exe)
	}
	return nil
}

// processImage returns the full path of a process's executable.
func processImage(pid uint32) (string, error) {
	p, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(p) //nolint:errcheck // best-effort cleanup of a query handle
	var buf [windows.MAX_LONG_PATH]uint16
	n := uint32(windows.MAX_LONG_PATH)
	if err := windows.QueryFullProcessImageName(p, 0, &buf[0], &n); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf[:n]), nil
}

// samePath compares two Windows paths, resolving links where it can.
func samePath(a, b string) bool {
	norm := func(p string) string {
		if r, err := finalPath(p); err == nil {
			p = r
		}
		return strings.ToLower(filepath.Clean(p))
	}
	return norm(a) == norm(b)
}

// createServerPipe creates the TUI-side end of the named pipe: duplex,
// byte mode (the codec already frames on newlines), a single instance since
// exactly one worker ever connects, and overlapped so
// [pipeConn.waitForConnect] can be given a deadline. It refuses remote
// clients, fails if any pipe of that name already exists (so a pipe made
// earlier by someone else is never mistaken for ours), and carries
// [workerPipeSDDL] rather than the default security.
func createServerPipe(name string) (windows.Handle, error) {
	return createPipeWithSDDL(name, workerPipeSDDL)
}

// createPipeWithSDDL is [createServerPipe] with the access list given. Only
// tests pass anything but [workerPipeSDDL].
func createPipeWithSDDL(name, sddl string) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return 0, fmt.Errorf("building the pipe's security: %w", err)
	}
	sa := &windows.SecurityAttributes{SecurityDescriptor: sd, InheritHandle: 0}
	sa.Length = uint32(unsafe.Sizeof(*sa))
	return windows.CreateNamedPipe(p,
		windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_OVERLAPPED|windows.FILE_FLAG_FIRST_PIPE_INSTANCE,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS,
		1, pipeBufferSize, pipeBufferSize, 0, sa)
}

// joinArgs builds a Windows command-line parameter string that
// CommandLineToArgvW (and so the worker's own flag parsing) splits back into
// exactly args: quotes, trailing backslashes and empty strings included.
func joinArgs(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = windows.EscapeArg(a)
	}
	return strings.Join(parts, " ")
}
