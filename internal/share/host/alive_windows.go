//go:build windows

package host

import (
	"time"

	"golang.org/x/sys/windows"
)

// stillActive is the exit code Windows reports for a process that has not
// exited.
const stillActive = 259

// processAlive opens the process, asks for its exit code, and, when
// startedAt is known, checks the process was created no later than that. A
// process created after the share started cannot be the Devpit that started
// it: Windows gave it the dead Devpit's PID.
func processAlive(pid int, startedAt time.Time) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid)) //nolint:gosec // PIDs are small positive numbers
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h) //nolint:errcheck // nothing to do about a failed close
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil || code != stillActive {
		return false
	}
	if startedAt.IsZero() {
		return true
	}
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return true // cannot tell: never clean up a share that may be alive
	}
	return sameOwner(time.Unix(0, created.Nanoseconds()), startedAt)
}
