//go:build windows

package adapters

import (
	"os/exec"
	"syscall"
)

// createNoWindow is CREATE_NO_WINDOW: a captured who-am-I run gets no
// console, so it can neither flash a window nor draw over the screens.
const createNoWindow = 0x08000000

func hideWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
