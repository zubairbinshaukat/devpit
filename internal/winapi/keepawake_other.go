//go:build !windows

package winapi

// keepAwake is not implemented outside Windows.
func keepAwake() (func(), error) { return func() {}, ErrUnsupported }
