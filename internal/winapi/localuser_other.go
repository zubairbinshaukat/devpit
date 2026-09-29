//go:build !windows

package winapi

import "time"

// createLocalUser is not implemented outside Windows.
func createLocalUser(string, string, time.Time) error { return ErrUnsupported }

// deleteLocalUser is not implemented outside Windows.
func deleteLocalUser(string) error { return ErrUnsupported }

// localUserSID is not implemented outside Windows.
func localUserSID(string) (string, error) { return "", ErrUnsupported }
