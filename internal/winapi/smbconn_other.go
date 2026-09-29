//go:build !windows

package winapi

// connectShare is not implemented outside Windows.
func connectShare(string, string, string) error { return ErrUnsupported }

// disconnectShare is not implemented outside Windows.
func disconnectShare(string, bool) error { return ErrUnsupported }
