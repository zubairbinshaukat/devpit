//go:build windows

package winapi

import (
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/windows"
)

// stillActive is the exit code Windows reports for a process that has not
// exited.
const stillActive = 259

func processStart(pid int) (time.Time, bool) {
	if pid <= 0 {
		return time.Time{}, false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid)) //nolint:gosec // PIDs are small positive numbers
	if err != nil {
		// Access denied means the process exists but is not ours to look at.
		return time.Time{}, errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer windows.CloseHandle(h) //nolint:errcheck // nothing to do about a failed close
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil || code != stillActive {
		return time.Time{}, false
	}
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return time.Time{}, true
	}
	return time.Unix(0, created.Nanoseconds()), true
}

func processImage(pid int) (string, error) {
	if pid <= 0 {
		return "", errors.New("winapi: no such process")
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid)) //nolint:gosec // PIDs are small positive numbers
	if err != nil {
		return "", fmt.Errorf("OpenProcess: %w", err)
	}
	defer windows.CloseHandle(h) //nolint:errcheck // nothing to do about a failed close
	var buf [windows.MAX_LONG_PATH]uint16
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return "", fmt.Errorf("QueryFullProcessImageName: %w", err)
	}
	return windows.UTF16ToString(buf[:size]), nil
}

func bootTime() (time.Time, error) {
	return time.Now().Add(-windows.DurationSinceBoot()), nil
}
