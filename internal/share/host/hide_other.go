//go:build !windows

package host

import "os/exec"

// hideWindow does nothing outside Windows.
func hideWindow(*exec.Cmd) {}
