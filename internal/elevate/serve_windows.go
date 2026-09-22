//go:build windows

package elevate

import (
	"context"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// Serve is the elevated worker's entry point. It dials the named pipe the
// TUI already created as pipeName (see [Launch]), announces hello, then
// runs [serveConn]'s loop until the TUI sends a shutdown request or the
// pipe breaks. cmd/devpit/worker.go is the only caller in production.
func Serve(ctx context.Context, pipeName string, exec Executor) error {
	name, err := windows.UTF16PtrFromString(pipeName)
	if err != nil {
		return err
	}
	// The TUI's end was created with FILE_FLAG_OVERLAPPED so its connect
	// wait could have a deadline; this end does not need that, so a plain
	// synchronous handle keeps the worker side simple enough to wrap in
	// *os.File directly.
	h, err := windows.CreateFile(name,
		windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil,
		windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return fmt.Errorf("elevate: connecting to %s: %w", pipeName, err)
	}
	conn := os.NewFile(uintptr(h), pipeName)
	defer conn.Close() //nolint:errcheck // the pipe is already done with once Serve returns

	elevated, _ := IsElevated()
	return serveConn(ctx, conn, exec, elevated, os.Getpid())
}
