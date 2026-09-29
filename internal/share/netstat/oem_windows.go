//go:build windows

package netstat

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// cpOEM is CP_OEMCP: the code page console programs print in.
const cpOEM = 1

// decodeOEM converts console output to UTF-8 with MultiByteToWideChar.
func decodeOEM(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	n, err := windows.MultiByteToWideChar(cpOEM, 0, &b[0], int32(len(b)), nil, 0) //nolint:gosec // console output of one command is far below 2 GB
	if err != nil || n == 0 {
		return string(b)
	}
	w := make([]uint16, n)
	if _, err := windows.MultiByteToWideChar(cpOEM, 0, &b[0], int32(len(b)), &w[0], n); err != nil { //nolint:gosec // as above
		return string(b)
	}
	return windows.UTF16ToString(w)
}

// hideWindow stops the child opening a console window.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
