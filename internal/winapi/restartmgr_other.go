//go:build !windows

package winapi

// LockHolders is not implemented outside Windows. There is no Restart Manager
// to ask, so callers fall back to reporting the lock without a holder name.
func LockHolders([]string) ([]LockHolder, error) { return nil, ErrUnsupported }
