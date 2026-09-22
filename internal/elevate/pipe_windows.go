//go:build windows

package elevate

import (
	"errors"
	"io"

	"golang.org/x/sys/windows"
)

// pipeConn wraps a named pipe handle created with FILE_FLAG_OVERLAPPED so
// that waiting for a client to connect can be given a deadline: a plain
// synchronous ConnectNamedPipe call cannot be cancelled from another
// goroutine without risking undefined behaviour, but an overlapped one can,
// via CancelIoEx. Every I/O on the handle has to stay overlapped once it is
// created that way, so Read and Write use the same pattern.
type pipeConn struct {
	h windows.Handle
}

// waitForConnect blocks until a client connects, or cancel fires first (in
// which case the pending connect is cancelled and an error returned).
func (p *pipeConn) waitForConnect(cancel <-chan struct{}) error {
	ev, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(ev) //nolint:errcheck // best-effort cleanup of a local event handle

	ov := &windows.Overlapped{HEvent: ev}
	err = windows.ConnectNamedPipe(p.h, ov)
	switch {
	case err == nil, errors.Is(err, windows.ERROR_PIPE_CONNECTED):
		// A client connected before or during the call itself; nothing is
		// actually pending.
		return nil
	case errors.Is(err, windows.ERROR_IO_PENDING):
		// Falls through to the wait below.
	default:
		return err
	}

	waitDone := make(chan struct{})
	go func() {
		select {
		case <-cancel:
			_ = windows.CancelIoEx(p.h, ov)
		case <-waitDone:
		}
	}()
	var n uint32
	getErr := windows.GetOverlappedResult(p.h, ov, &n, true)
	close(waitDone)
	return getErr
}

// io performs one overlapped Read or Write and waits for it to complete.
func (p *pipeConn) io(b []byte, write bool) (int, error) {
	ev, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(ev) //nolint:errcheck // best-effort cleanup of a local event handle

	ov := &windows.Overlapped{HEvent: ev}
	var n uint32
	if write {
		err = windows.WriteFile(p.h, b, &n, ov)
	} else {
		err = windows.ReadFile(p.h, b, &n, ov)
	}

	if err != nil && !errors.Is(err, windows.ERROR_IO_PENDING) {
		return int(n), translatePipeErr(err, write)
	}
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		getErr := windows.GetOverlappedResult(p.h, ov, &n, true)
		if getErr != nil {
			return int(n), translatePipeErr(getErr, write)
		}
	}
	return int(n), nil
}

// translatePipeErr turns the far side of the pipe closing into io.EOF on
// reads, the shape every io.Reader consumer already expects.
func translatePipeErr(err error, write bool) error {
	if write {
		return err
	}
	if errors.Is(err, windows.ERROR_BROKEN_PIPE) || errors.Is(err, windows.ERROR_PIPE_NOT_CONNECTED) {
		return io.EOF
	}
	return err
}

func (p *pipeConn) Read(b []byte) (int, error)  { return p.io(b, false) }
func (p *pipeConn) Write(b []byte) (int, error) { return p.io(b, true) }

// Close cancels any I/O in flight on the handle before closing it, so a
// goroutine blocked in Read or Write wakes up instead of hanging forever.
func (p *pipeConn) Close() error {
	_ = windows.CancelIoEx(p.h, nil)
	return windows.CloseHandle(p.h)
}
