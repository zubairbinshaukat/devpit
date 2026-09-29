// Package netstat is the receiving PC's window on the network: it checks the
// address the user typed, lists the shares of another PC, signs in, closes
// old sign-ins, probes whether the other PC answers, and measures speed from
// the adapter's own byte counters.
//
// Windows prints the output of `net view` and `net use` in the language of
// the PC, in the OEM code page. So this package reads their shape, never
// their words, and takes error numbers from Win32 calls where it can.
package netstat

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
)

// hostName is what a host may look like when it is not an IP: letters,
// digits, dots and dashes, as in a DNS name or a Windows PC name. Nothing
// else ever reaches a command line or a UNC path.
var hostName = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

// ErrBadHost means the address is neither an IPv4 address nor a PC name.
var ErrBadHost = errors.New("that does not look like an IP address or PC name")

// ParseHost cleans up and checks what the user typed. It accepts an IPv4
// address, an IPv6 address or a PC name, with or without leading \\ and
// surrounding spaces, and returns it in the form used in a UNC path. An IPv6
// address is written with dashes and the .ipv6-literal.net suffix, the form
// Windows accepts in UNC paths.
func ParseHost(s string) (string, error) {
	h := strings.TrimSpace(s)
	h = strings.TrimLeft(h, `\/`)
	if i := strings.IndexAny(h, `\/`); i >= 0 {
		h = h[:i]
	}
	if h == "" {
		return "", fmt.Errorf("type the other PC's IP address: %w", ErrBadHost)
	}
	if ip := net.ParseIP(h); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return v4.String(), nil
		}
		if ip.IsLinkLocalUnicast() {
			return "", fmt.Errorf("link-local IPv6 needs a zone; use the IPv4 address: %w", ErrBadHost)
		}
		r := strings.NewReplacer(":", "-")
		return r.Replace(ip.String()) + ".ipv6-literal.net", nil
	}
	if !hostName.MatchString(h) {
		return "", fmt.Errorf("%q: %w", h, ErrBadHost)
	}
	return h, nil
}

// UNC returns \\host\share, or \\host when share is empty.
func UNC(host, share string) string {
	if share == "" {
		return `\\` + host
	}
	return `\\` + host + `\` + share
}
