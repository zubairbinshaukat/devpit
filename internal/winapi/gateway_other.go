//go:build !windows

package winapi

// interfaceGateway is not implemented outside Windows.
func interfaceGateway(string) (string, error) { return "", ErrUnsupported }
