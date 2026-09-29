//go:build !windows

package netstat

import "os/exec"

// decodeOEM returns the bytes as they are: outside Windows the console is
// UTF-8 already.
func decodeOEM(b []byte) string { return string(b) }

// hideWindow does nothing outside Windows.
func hideWindow(*exec.Cmd) {}
