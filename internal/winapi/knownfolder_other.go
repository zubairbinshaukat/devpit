//go:build !windows

package winapi

func downloadsFolder() (string, error) { return "", ErrUnsupported }
