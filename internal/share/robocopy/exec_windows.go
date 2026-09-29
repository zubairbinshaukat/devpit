//go:build windows

package robocopy

import (
	"os/exec"
	"syscall"
)

// createNoWindow is CREATE_NO_WINDOW, so a console tool started from the
// terminal UI does not flash a window of its own.
const createNoWindow = 0x08000000

// hideWindow stops the child opening a console window.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
