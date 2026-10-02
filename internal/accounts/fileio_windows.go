//go:build windows

package accounts

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// openShared opens path for reading with FILE_SHARE_DELETE as well as read
// and write sharing. os.Open leaves FILE_SHARE_DELETE out, and a file open
// without it cannot be renamed over: a shim reading accounts.toml for a
// millisecond would make Devpit's atomic save fail with "Access is denied".
func openShared(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}

// transient reports whether a failed rename or open is worth trying again.
func transient(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
		errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
