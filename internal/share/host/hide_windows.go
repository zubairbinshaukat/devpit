//go:build windows

package host

import (
	"os/exec"
	"syscall"
)

// hideWindow stops the child opening a console window.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
