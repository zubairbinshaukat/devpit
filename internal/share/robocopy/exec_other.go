//go:build !windows

package robocopy

import "os/exec"

// hideWindow does nothing outside Windows, where a child has no console
// window to hide.
func hideWindow(*exec.Cmd) {}
