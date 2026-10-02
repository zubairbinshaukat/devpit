//go:build !windows

package accounts

import "os"

// openShared opens path for reading. Only Windows needs the sharing flags.
func openShared(path string) (*os.File, error) { return os.Open(path) } // #nosec G304 -- Devpit's own file

// transient is never true off Windows: a rename there does not fail because
// another process is reading the file.
func transient(error) bool { return false }
