//go:build !windows

package ports

import "errors"

// Kill is not implemented outside Windows.
func Kill(uint32) error {
	return errors.ErrUnsupported
}

// KillTree is not implemented outside Windows.
func KillTree(uint32) error {
	return errors.ErrUnsupported
}
