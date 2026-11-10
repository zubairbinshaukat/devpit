// Package host is the sharing side of file sharing: pick an adapter, set up
// a read-only share with a temporary login through the elevated worker, show
// what the other PC needs, and take everything down again, on Stop, on quit,
// on a crash and on the next launch.
//
// The package has no Bubble Tea import. The elevated worker, the clock, the
// random source, the network view and the disk are all injected, so the
// whole life of a share is tested on fakes.
package host

import (
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"strings"
)

// PasswordLength is 6 digits. This is a deliberate trade-off for a code that
// is easy to read out and type on another PC: 10^6 possibilities is about 20
// bits, which is weak. It is acceptable only because the login is short-lived
// (the account is removed when sharing stops and expires on its own), read-only
// and meant for people you trust on your own network. Windows' account lockout
// is the only throttle on guesses, and someone on the same network who captures
// a sign-in handshake could try all one million codes offline.
const PasswordLength = 6

// digitSet is what the password is made of.
const digitSet = "0123456789"

// Password returns PasswordLength random digits from r (crypto/rand in
// production), each drawn uniformly with no modulo bias. Leading zeros are
// allowed. It is never the user's own Windows password and is made fresh for
// every share. The user name is not needed to build it; the parameter is kept
// so callers need not change.
func Password(r io.Reader, _ string) (string, error) {
	out := make([]byte, PasswordLength)
	for i := range out {
		c, err := pick(r, digitSet)
		if err != nil {
			return "", err
		}
		out[i] = c
	}
	return string(out), nil
}

// pick returns one random byte of set.
func pick(r io.Reader, set string) (byte, error) {
	i, err := index(r, len(set))
	if err != nil {
		return 0, err
	}
	return set[i], nil
}

// index returns a uniform random number in [0, n) with no modulo bias.
func index(r io.Reader, n int) (int, error) {
	v, err := rand.Int(r, big.NewInt(int64(n)))
	if err != nil {
		return 0, fmt.Errorf("reading random numbers: %w", err)
	}
	return int(v.Int64()), nil
}

// suffixAlphabet is what the random part of a user or share name is made of.
const suffixAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// suffix returns n random characters of [suffixAlphabet].
func suffix(r io.Reader, n int) (string, error) {
	b := make([]byte, n)
	for i := range b {
		c, err := pick(r, suffixAlphabet)
		if err != nil {
			return "", err
		}
		b[i] = c
	}
	return string(b), nil
}

// UserName returns a fresh temporary account name, devpit-xxxx. The worker
// only ever creates or deletes accounts of exactly this shape.
func UserName(r io.Reader) (string, error) {
	s, err := suffix(r, 4)
	if err != nil {
		return "", err
	}
	return "devpit-" + s, nil
}

// HotspotPasswordLength is the temporary hotspot password: 8 digits, the
// shortest password WPA2 allows (owner decision 19). It is displayable: shown
// on the card and in the WIFI: string, never on disk, in a log or on a
// command line.
const HotspotPasswordLength = 8

// HotspotPassword returns HotspotPasswordLength random digits from r
// (crypto/rand in production), new for every share.
func HotspotPassword(r io.Reader) (string, error) {
	out := make([]byte, HotspotPasswordLength)
	for i := range out {
		c, err := pick(r, digitSet)
		if err != nil {
			return "", err
		}
		out[i] = c
	}
	return string(out), nil
}

// HotspotSSID is the temporary hotspot name for the account user:
// "Devpit-" and the same characters as the account's "devpit-xxxx", so the
// other PC can find the hotspot and fill in the user name.
func HotspotSSID(user string) string {
	return "Devpit-" + strings.TrimPrefix(user, "devpit-")
}

// wifiEscaper escapes the characters the WIFI: format reserves.
var wifiEscaper = strings.NewReplacer(`\`, `\\`, `;`, `\;`, `,`, `\,`, `"`, `\"`, `:`, `\:`)

// WifiString is the standard Wi-Fi join string, WIFI:T:WPA;S:<ssid>;P:<pass>;;
// with \ ; , " and : escaped, for a phone or a PC without Devpit.
func WifiString(ssid, pass string) string {
	return "WIFI:T:WPA;S:" + wifiEscaper.Replace(ssid) + ";P:" + wifiEscaper.Replace(pass) + ";;"
}

// unsafeName matches everything a share name may not contain here.
var unsafeName = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// ShareName turns a folder's name into a share name: letters, digits, dash
// and underscore only, cut to 24 characters, with a short random tail so it
// never collides with a share the user already has. A folder named "My Games"
// becomes "My-Games-x7k2".
func ShareName(r io.Reader, folder string) (string, error) {
	base := strings.Trim(unsafeName.ReplaceAllString(folder, "-"), "-_")
	if len(base) > 24 {
		base = strings.Trim(base[:24], "-_")
	}
	if base == "" {
		base = "Share"
	}
	s, err := suffix(r, 4)
	if err != nil {
		return "", err
	}
	return base + "-" + s, nil
}
