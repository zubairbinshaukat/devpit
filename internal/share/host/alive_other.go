//go:build !windows

package host

import (
	"os"
	"syscall"
	"time"
)

// processAlive sends signal 0, which checks the process exists without
// disturbing it. Devpit ships for Windows; this build only has to compile and
// be sensible, so it does not compare start times.
func processAlive(pid int, _ time.Time) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
