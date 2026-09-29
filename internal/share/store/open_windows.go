//go:build windows

package store

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// openShared opens path for reading with FILE_SHARE_DELETE as well as read
// and write sharing. os.Open leaves FILE_SHARE_DELETE out, and on Windows a
// file that is open without it cannot be renamed over: a reader that happens
// to hold the record for a millisecond makes the writer's atomic replace
// fail with "Access is denied". With it, the writer's rename goes through and
// the reader keeps reading the old contents.
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

// transient reports whether a failed rename or open is worth trying again:
// another process (an antivirus scanner, the search indexer, or a second
// Devpit reading the record) has the file open for a moment.
func transient(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
		errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
