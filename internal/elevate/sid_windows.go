//go:build windows

package elevate

import (
	"errors"

	"golang.org/x/sys/windows"
)

// lookupSIDAccount returns the account name sid belongs to on this PC, or
// mapped=false when no account has it any more (a deleted account's SID).
func lookupSIDAccount(sid string) (name string, mapped bool, err error) {
	s, err := windows.StringToSid(sid)
	if err != nil {
		return "", false, err
	}
	account, _, _, err := s.LookupAccount("")
	if errors.Is(err, windows.ERROR_NONE_MAPPED) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return account, true, nil
}
