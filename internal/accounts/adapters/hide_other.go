//go:build !windows

package adapters

import "os/exec"

// hideWindow does nothing outside Windows: there are no console windows.
func hideWindow(*exec.Cmd) {}
