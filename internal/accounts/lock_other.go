//go:build !windows

package accounts

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// staleLock is how old a lock file must be before another process may take
// it over. Devpit is a Windows program; this exists so the pure-logic tests
// run on the Linux CI runner.
const staleLock = 30 * time.Second

// tryLock creates the lock file exclusively.
func tryLock(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- Devpit's own lock file
	if err == nil {
		return &Lock{path: path, f: f}, nil
	}
	if !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	if fi, serr := os.Stat(path); serr == nil && time.Since(fi.ModTime()) > staleLock {
		_ = os.Remove(path)
		return tryLock(path)
	}
	return nil, ErrLocked
}

func unlock(l *Lock) {
	_ = l.f.Close()
	_ = os.Remove(l.path)
}
