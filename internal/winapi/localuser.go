package winapi

import "time"

// CreateLocalUser makes a local Windows account that can sign in over the
// network: a plain user with a password that never changes, expiring on the
// given time as a safety net for the day a cleanup never runs. The password
// goes to Windows in memory, never through a command line. The account is
// also left off the sign-in screen (Winlogon SpecialAccounts\UserList), and
// [DeleteLocalUser] takes that entry away again.
func CreateLocalUser(name, password string, expires time.Time) error {
	return createLocalUser(name, password, expires)
}

// DeleteLocalUser removes a local account. A missing account is an error the
// caller may treat as success.
func DeleteLocalUser(name string) error { return deleteLocalUser(name) }

// LocalUserSID returns the security identifier of a local account as text,
// e.g. "S-1-5-21-...-1004".
func LocalUserSID(name string) (string, error) { return localUserSID(name) }
