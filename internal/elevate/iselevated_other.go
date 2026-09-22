//go:build !windows

package elevate

// IsElevated always reports false outside Windows: Devpit's elevation
// subsystem, like the rest of Devpit, only exists on Windows.
func IsElevated() (bool, error) { return false, nil }
