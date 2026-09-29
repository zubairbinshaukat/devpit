package winapi

// ConnectShare opens a session to a network share with the given account,
// without ever putting the password on a command line. remote is a UNC path
// such as \\192.168.1.5\Games; user is "name", "domain\name" or
// "MicrosoftAccount\someone@example.com".
//
// The session is temporary: it is gone at sign-out and Devpit removes it
// itself when the copy ends. A failure is a [syscall.Errno] holding the Win32
// code (1219, 1326, 53 and so on), which is what makes the error messages
// independent of the Windows language.
func ConnectShare(remote, user, password string) error { return connectShare(remote, user, password) }

// DisconnectShare closes the session to remote. force closes it even with
// files open.
func DisconnectShare(remote string, force bool) error { return disconnectShare(remote, force) }
