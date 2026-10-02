//go:build !windows

package protect

import "path/filepath"

// platformCanon has nothing to add off Windows: there are no short names and
// no trailing-dot rewriting.
func platformCanon(p string) string { return p }

// finalPath resolves an existing path through its symlinks.
func finalPath(p string) (string, bool) {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", false
	}
	return r, true
}
