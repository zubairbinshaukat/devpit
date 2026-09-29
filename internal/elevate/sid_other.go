//go:build !windows

package elevate

import "errors"

// lookupSIDAccount has no accounts to look in outside Windows.
func lookupSIDAccount(string) (string, bool, error) {
	return "", false, errors.ErrUnsupported
}
