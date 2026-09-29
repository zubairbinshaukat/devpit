//go:build !windows

package host

import (
	"os"
	"syscall"
)

// processAlive sends signal 0, which checks the process exists without
// disturbing it.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
