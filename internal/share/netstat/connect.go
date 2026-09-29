package netstat

import (
	"context"
	"fmt"
	"strings"
)

// Connector opens and closes sessions to a share. The real one calls
// WNetAddConnection2 and WNetCancelConnection2, which take the password in
// memory. The alternative, `net use \\host\share password /user:name`, puts
// the password on a command line that other processes can read, and `net use
// ... *` reads it from the console, which cannot be driven from a pipe. So
// the API is used, and it also returns real error numbers instead of
// translated text.
type Connector interface {
	// Connect signs in to remote (\\host\share) as user with password. The
	// error is a syscall.Errno with the Win32 code.
	Connect(remote, user, password string) error
	// Disconnect closes the session to remote, even with files open.
	Disconnect(remote string) error
}

// Credentials are what the user typed to sign in.
type Credentials struct {
	// User is "name", "domain\name" or "MicrosoftAccount\e-mail".
	User string
	// Password is kept in memory only and never written to disk or logged.
	Password string
}

// String hides the password, so a credential can never leak through a log
// line or a %v by accident.
func (c Credentials) String() string { return "Credentials{" + c.User + ", password hidden}" }

// GoString hides the password for %#v too.
func (c Credentials) GoString() string { return c.String() }

// NormalizeUser tidies a typed user name. Surrounding spaces go, and a lone
// backslash prefix is dropped. Microsoft account e-mails are not rewritten
// here: [errmap] explains the missing prefix when the sign-in fails, and the
// screen offers it.
func NormalizeUser(u string) string {
	u = strings.TrimSpace(u)
	return strings.TrimPrefix(u, `\`)
}

// WithMicrosoftPrefix returns user in the MicrosoftAccount\ form Windows
// wants for a Microsoft account e-mail, and user unchanged when it already
// has a domain part.
func WithMicrosoftPrefix(user string) string {
	if strings.Contains(user, `\`) {
		return user
	}
	return `MicrosoftAccount\` + user
}

// SignIn opens a session to \\host\share with creds. The password reaches
// Windows in memory only.
func SignIn(c Connector, host, share string, creds Credentials) error {
	if err := c.Connect(UNC(host, share), NormalizeUser(creds.User), creds.Password); err != nil {
		return fmt.Errorf("signing in to %s: %w", UNC(host, share), err)
	}
	return nil
}

// DropConnections closes every session this PC has open to host. Windows
// allows one set of credentials per PC, so a leftover session made with
// another account (error 1219) has to go before the new one can be opened.
// It returns how many were closed.
func DropConnections(ctx context.Context, r Runner, c Connector, host string) (int, error) {
	list, err := ConnectionsTo(ctx, r, host)
	if err != nil {
		return 0, err
	}
	closed := 0
	for _, remote := range list {
		if err := c.Disconnect(remote); err != nil {
			return closed, fmt.Errorf("closing %s: %w", remote, err)
		}
		closed++
	}
	return closed, nil
}
