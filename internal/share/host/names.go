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
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"strings"
)

// The alphabet of the password. It has upper case, lower case and digits, so
// it meets Windows' complexity rule of three of four classes, and it has no
// symbol, so the password is safe in a `net use` line typed into cmd or
// PowerShell. The look-alike letters (I, l, O, 0, 1) are left out because
// people read the password off a screen and type it on another PC.
const (
	upper  = "ABCDEFGHJKLMNPQRSTUVWXYZ"
	lower  = "abcdefghijkmnopqrstuvwxyz"
	digits = "23456789"
)

// PasswordLength is 16 characters, about 92 bits.
const PasswordLength = 16

// Password returns a random password from crypto/rand, with at least two of
// each class and never containing the user name. It is never the user's own
// Windows password, and it is made fresh for every share.
func Password(r io.Reader, user string) (string, error) {
	all := upper + lower + digits
	for range 20 {
		out := make([]byte, 0, PasswordLength)
		for _, set := range []string{upper, upper, lower, lower, digits, digits} {
			c, err := pick(r, set)
			if err != nil {
				return "", err
			}
			out = append(out, c)
		}
		for len(out) < PasswordLength {
			c, err := pick(r, all)
			if err != nil {
				return "", err
			}
			out = append(out, c)
		}
		// Shuffle so the guaranteed characters are not always first.
		for i := len(out) - 1; i > 0; i-- {
			j, err := index(r, i+1)
			if err != nil {
				return "", err
			}
			out[i], out[j] = out[j], out[i]
		}
		pw := string(out)
		if !strings.Contains(strings.ToLower(pw), strings.ToLower(user)) {
			return pw, nil
		}
	}
	return "", errors.New("could not make a password")
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
