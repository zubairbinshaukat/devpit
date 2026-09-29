//go:build windows

package elevate

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/windows"
)

// pipeConn wraps a named pipe handle opened with FILE_FLAG_OVERLAPPED. Both
// ends use it: the TUI's, so that waiting for the worker to connect can be
// given a deadline, and the worker's, because the worker reads its next
// request while its jobs write their output. On a handle opened without
// FILE_FLAG_OVERLAPPED Windows runs one I/O at a time, so a write waits for
// the read in progress to finish, which on this protocol is never: the
// first output line of the first job would hang the worker. Overlapped I/O
// has no such lock, and each Read and Write waits for its own completion.
type pipeConn struct {
	h windows.Handle
	// closing is a manual-reset event Close sets, so an I/O that is waiting
	// wakes up and cancels itself even if it started after Close's own
	// CancelIoEx.
	closing windows.Handle

	// mu is held for reading by every I/O and for writing by Close, so the
	// handle is never closed (and its value never reused) under an I/O.
	mu        sync.RWMutex
	closed    atomic.Bool
	closeOnce sync.Once
	closeErr  error
}

// newPipeConn wraps h, which must have been opened with
// FILE_FLAG_OVERLAPPED. It takes ownership of h: on failure h is closed.
func newPipeConn(h windows.Handle) (*pipeConn, error) {
	ev, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		_ = windows.CloseHandle(h)
		return nil, err
	}
	return &pipeConn{h: h, closing: ev}, nil
}

// errPipeClosed is what an I/O on a closed pipeConn returns.
var errPipeClosed = io.ErrClosedPipe

// waitForConnect blocks until a client connects, or cancel fires first (in
// which case the pending connect is cancelled and an error returned).
func (p *pipeConn) waitForConnect(cancel <-chan struct{}) error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed.Load() {
		return errPipeClosed
	}
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

// io performs one overlapped Read or Write and waits for it to complete, or
// for Close, whichever comes first.
func (p *pipeConn) io(b []byte, write bool) (int, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed.Load() {
		return 0, errPipeClosed
	}
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
	switch {
	case err == nil:
		return int(n), nil
	case !errors.Is(err, windows.ERROR_IO_PENDING):
		return int(n), translatePipeErr(err, write)
	}

	// Pending: wait for it, or for Close, which cancels it. Either way
	// GetOverlappedResult then waits for the kernel to be done with ov and
	// b before they can go out of scope.
	which, _ := windows.WaitForMultipleObjects([]windows.Handle{ev, p.closing}, false, windows.INFINITE)
	if which == windows.WAIT_OBJECT_0+1 {
		_ = windows.CancelIoEx(p.h, ov)
	}
	if getErr := windows.GetOverlappedResult(p.h, ov, &n, true); getErr != nil {
		if p.closed.Load() && errors.Is(getErr, windows.ERROR_OPERATION_ABORTED) {
			return int(n), errPipeClosed
		}
		return int(n), translatePipeErr(getErr, write)
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

// Close wakes every I/O in flight on the handle, waits for them to return,
// and only then closes it, so a goroutine blocked in Read or Write wakes up
// instead of hanging, and none can touch the handle after it is gone. It is
// safe to call more than once.
func (p *pipeConn) Close() error {
	p.closeOnce.Do(func() {
		p.closed.Store(true)
		_ = windows.SetEvent(p.closing)
		_ = windows.CancelIoEx(p.h, nil)
		p.mu.Lock()
		p.closeErr = windows.CloseHandle(p.h)
		_ = windows.CloseHandle(p.closing)
		p.mu.Unlock()
	})
	return p.closeErr
}
