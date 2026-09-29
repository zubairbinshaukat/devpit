//go:build !windows

package winapi

// fileSystem is not implemented outside Windows.
func fileSystem(string) (string, error) { return "", ErrUnsupported }
