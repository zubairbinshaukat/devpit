//go:build windows

package accounts

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// tryLock opens the lock file and takes an exclusive byte-range lock on it
// without waiting. The lock belongs to the handle: closing it, or the
// process ending, releases it.
func tryLock(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) // #nosec G304 -- Devpit's own lock file
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	var ol windows.Overlapped
	err = windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &ol)
	if err != nil {
		_ = f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("locking %s: %w", path, err)
	}
	return &Lock{path: path, f: f}, nil
}

func unlock(l *Lock) {
	var ol windows.Overlapped
	_ = windows.UnlockFileEx(windows.Handle(l.f.Fd()), 0, 1, 0, &ol)
	_ = l.f.Close()
}
