//go:build !windows

package winapi

// wifiInterfaces is not implemented outside Windows.
func wifiInterfaces() ([]WifiInterface, error) { return nil, ErrUnsupported }
