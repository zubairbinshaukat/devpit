//go:build windows

package elevate

import "golang.org/x/sys/windows"

// IsElevated reports whether the current process's token is already
// elevated from a UAC perspective. Devpit's TUI checks this only to decide
// whether it can skip launching a worker at all (for example, a developer
// who already opened an elevated terminal); the TUI itself never elevates.
func IsElevated() (bool, error) {
	return windows.GetCurrentProcessToken().IsElevated(), nil
}
