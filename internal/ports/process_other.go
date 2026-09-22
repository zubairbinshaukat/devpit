//go:build !windows

package ports

import "errors"

// Lookup is not implemented outside Windows.
func Lookup(uint32) (Process, error) {
	return Process{}, errors.ErrUnsupported
}

// Tree is not implemented outside Windows; it returns no descendants rather
// than an error since its signature carries none.
func Tree(uint32) []Process {
	return nil
}

// isSession0 is not implemented outside Windows.
func isSession0(uint32) (bool, error) {
	return false, errors.ErrUnsupported
}
