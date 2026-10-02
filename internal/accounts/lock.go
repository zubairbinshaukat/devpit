package accounts

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrLocked is returned when another Devpit is changing accounts. The second
// Devpit can still read everything; it just cannot change anything until the
// first one is done.
var ErrLocked = errors.New("another Devpit window is changing accounts right now; this one can look but not change anything until it is done")

// Lock is an exclusive hold on the accounts files. Windows releases it by
// itself if the process dies, so a crash never leaves the store locked.
type Lock struct {
	path string
	f    *os.File
}

// AcquireLock takes the lock at path, waiting up to wait for another holder
// to let go. It returns [ErrLocked] if it could not.
func AcquireLock(path string, wait time.Duration) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	deadline := time.Now().Add(wait)
	pause := 10 * time.Millisecond
	for {
		l, err := tryLock(path)
		if err == nil {
			return l, nil
		}
		if !errors.Is(err, ErrLocked) || time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(pause)
		pause = min(pause*2, 200*time.Millisecond)
	}
}

// Release lets go of the lock. It is safe on a nil Lock and more than once.
func (l *Lock) Release() {
	if l == nil || l.f == nil {
		return
	}
	unlock(l)
	l.f = nil
}
