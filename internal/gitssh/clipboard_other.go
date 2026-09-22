//go:build !windows

package gitssh

import "errors"

// CopyWin32 is not implemented outside Windows.
func CopyWin32(string) error { return errors.ErrUnsupported }
