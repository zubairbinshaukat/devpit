//go:build !windows

package elevate

import "path/filepath"

// finalPath returns p with every symbolic link resolved.
func finalPath(p string) (string, error) { return filepath.EvalSymlinks(p) }
