//go:build !windows

package clean

import "errors"

// moveToTrash is the Recycle Bin path, and the Recycle Bin is a Windows idea.
// Devpit is a Windows tool: the non-Windows build exists so the package can be
// vetted, linted and unit-tested on any machine, not so it can delete anything
// there. Rather than reach for a cross-platform trash library — one more
// dependency with one more way to remove a file, on a platform Devpit does not
// ship to — this returns [errors.ErrUnsupported] and removes nothing.
func moveToTrash(string) error {
	return errors.ErrUnsupported
}
