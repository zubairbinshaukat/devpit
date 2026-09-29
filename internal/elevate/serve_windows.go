//go:build windows

package elevate

import (
	"context"
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// Serve is the elevated worker's entry point. It dials the named pipe the
// TUI already created as pipeName (see [Launch]), checks that the pipe's
// server really is the TUI that launched it, announces hello, then runs
// [serveConn]'s loop until the TUI sends a shutdown request or the pipe
// breaks. cmd/devpit/worker.go is the only caller in production.
//
// The server check is what stops another program from driving an elevated
// worker: the worker runs as administrator and does what its pipe peer asks,
// so a pipe of the same name made by anyone else (after the TUI's own closed,
// say) must never be served. The name carries the TUI's PID, and the pipe's
// server has to be that process, alive, and older than the worker.
func Serve(ctx context.Context, pipeName string, exec Executor) error {
	wantPID, err := pipeServerPID(pipeName)
	if err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(pipeName)
	if err != nil {
		return err
	}
	// Overlapped, because the loop reads the next request while jobs write
	// their output; see [pipeConn]. SECURITY_SQOS_PRESENT|SECURITY_ANONYMOUS
	// means the pipe's server can never impersonate this elevated process's
	// token.
	h, err := windows.CreateFile(name,
		windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_OVERLAPPED|windows.SECURITY_SQOS_PRESENT|windows.SECURITY_ANONYMOUS, 0)
	if err != nil {
		return fmt.Errorf("elevate: connecting to %s: %w", pipeName, err)
	}
	if verr := verifyServer(h, wantPID); verr != nil {
		_ = windows.CloseHandle(h)
		return verr
	}
	conn, err := newPipeConn(h)
	if err != nil {
		return fmt.Errorf("elevate: connecting to %s: %w", pipeName, err)
	}
	defer conn.Close() //nolint:errcheck // the pipe is already done with once Serve returns

	elevated, _ := IsElevated()
	return serveConn(ctx, conn, exec, elevated, os.Getpid())
}

// verifyServer refuses a pipe whose server is not process wantPID, or is a
// process created after this one (a recycled PID).
func verifyServer(h windows.Handle, wantPID uint32) error {
	var pid uint32
	if err := windows.GetNamedPipeServerProcessId(h, &pid); err != nil {
		return fmt.Errorf("elevate: refused: cannot tell who owns the pipe: %w", err)
	}
	if pid != wantPID {
		return fmt.Errorf("elevate: refused: the pipe belongs to process %d, not to Devpit (%d)", pid, wantPID)
	}
	server, err := processCreated(pid)
	if err != nil {
		return fmt.Errorf("elevate: refused: cannot check process %d: %w", pid, err)
	}
	self, err := processCreated(uint32(os.Getpid())) //nolint:gosec // a Windows PID is a DWORD
	if err != nil {
		return fmt.Errorf("elevate: refused: cannot check this process: %w", err)
	}
	if !startedBefore(server, self) {
		return errors.New("elevate: refused: the pipe's owner started after this worker, so it did not launch it")
	}
	return nil
}

// processCreated returns a process's creation time in 100ns ticks.
func processCreated(pid uint32) (int64, error) {
	p, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(p) //nolint:errcheck // best-effort cleanup of a query handle
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(p, &created, &exited, &kernel, &user); err != nil {
		return 0, err
	}
	return created.Nanoseconds() / 100, nil
}
