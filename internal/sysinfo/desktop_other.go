//go:build !windows

package sysinfo

// DesktopDir is unsupported off Windows.
func DesktopDir() (string, error) { return "", ErrUnsupported }
