//go:build windows

package host

import "golang.org/x/sys/windows"

// stillActive is the exit code Windows reports for a process that has not
// exited.
const stillActive = 259

// processAlive opens the process and asks for its exit code.
func processAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid)) //nolint:gosec // PIDs are small positive numbers
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h) //nolint:errcheck // nothing to do about a failed close
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}
