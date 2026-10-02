package accounts

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// DefaultName is the reserved name of the account each tool was signed in to
// before Devpit: the tool's own login, in the tool's own folder. It cannot be
// created, renamed or removed, and it has no folder of Devpit's.
const DefaultName = "default"

// MaxNameLen is the longest account name. Names become folder names, so they
// are kept short.
const MaxNameLen = 32

// Errors returned by name checks. Each is wrapped with a sentence that names
// the offending input.
var (
	ErrReservedName = errors.New(`"default" is reserved for the account each tool was signed in to before Devpit`)
	ErrInvalidName  = errors.New("an account name may use letters, numbers and dashes, and must start and end with a letter or number")
	ErrNameTaken    = errors.New("that name is already used for this tool")
)

// IsDefault reports whether name is the reserved default name, in any case.
func IsDefault(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), DefaultName)
}

// ValidateName checks that name can be given to a new account: 1 to
// [MaxNameLen] ASCII letters, digits and dashes, starting and ending with a
// letter or digit, and not "default" in any case.
func ValidateName(name string) error {
	if IsDefault(name) {
		return fmt.Errorf("%w", ErrReservedName)
	}
	if !validName(name) {
		return fmt.Errorf("%q: %w (at most %d characters)", name, ErrInvalidName, MaxNameLen)
	}
	return nil
}

// validName is the pattern alone, without the reserved-name check.
func validName(name string) bool {
	if name == "" || len(name) > MaxNameLen {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-' && i > 0 && i < len(name)-1:
		default:
			return false
		}
	}
	return true
}

// AmbiguousNameError is returned when a short form matches more than one
// account. Matches lists every candidate so the message can name them.
type AmbiguousNameError struct {
	Input   string
	Matches []string
}

func (e *AmbiguousNameError) Error() string {
	return fmt.Sprintf("%q matches more than one account: %s. Type more of the name", e.Input, strings.Join(e.Matches, ", "))
}

// UnknownNameError is returned when nothing matches. Known lists what does
// exist, so the message can offer it.
type UnknownNameError struct {
	Input string
	Known []string
}

func (e *UnknownNameError) Error() string {
	if len(e.Known) == 0 {
		return fmt.Sprintf("there is no account called %q", e.Input)
	}
	return fmt.Sprintf("there is no account called %q. Known accounts: %s", e.Input, strings.Join(e.Known, ", "))
}

// MatchName finds the one name in names that input refers to. An exact
// match, ignoring case, always wins; otherwise input must be the start of
// exactly one name ("sec" finds "second"). The returned name is spelled as it
// is in names.
func MatchName(input string, names []string) (string, error) {
	in := strings.TrimSpace(input)
	if in == "" {
		return "", &UnknownNameError{Input: input, Known: names}
	}
	for _, n := range names {
		if strings.EqualFold(n, in) {
			return n, nil
		}
	}
	var hits []string
	low := strings.ToLower(in)
	for _, n := range names {
		if strings.HasPrefix(strings.ToLower(n), low) {
			hits = append(hits, n)
		}
	}
	switch len(hits) {
	case 0:
		return "", &UnknownNameError{Input: input, Known: names}
	case 1:
		return hits[0], nil
	}
	return "", &AmbiguousNameError{Input: input, Matches: hits}
}

// webmail is the set of domains whose name says nothing about which account
// it is, so a suggestion comes from the address itself instead.
func webmail(domain string) bool {
	switch domain {
	case "gmail.com", "googlemail.com", "outlook.com", "hotmail.com", "live.com",
		"msn.com", "yahoo.com", "ymail.com", "icloud.com", "me.com", "mac.com",
		"proton.me", "protonmail.com", "pm.me", "aol.com", "gmx.com", "gmx.net",
		"gmx.de", "yandex.com", "yandex.ru", "zoho.com", "fastmail.com", "hey.com",
		"mail.com", "qq.com", "163.com", "tutanota.com", "duck.com":
		return true
	}
	return strings.HasSuffix(domain, ".noreply.github.com") || domain == "users.noreply.github.com"
}

// SuggestName proposes a name for an account from its email address:
// zubair@work.com → "work"; for a webmail address the part before the @
// (zubair.s@gmail.com → "zubair-s", or the tag in zubair+oss@gmail.com →
// "oss"); for a GitHub noreply address the login. A suggestion that is
// taken (ignoring case) or reserved gets "-2", "-3"… The result always
// passes [ValidateName].
func SuggestName(email string, taken []string) string {
	base := suggestBase(strings.ToLower(strings.TrimSpace(email)))
	if base == "" {
		base = "account"
	}
	isTaken := func(n string) bool {
		if IsDefault(n) {
			return true
		}
		for _, t := range taken {
			if strings.EqualFold(t, n) {
				return true
			}
		}
		return false
	}
	if !isTaken(base) {
		return base
	}
	for i := 2; ; i++ {
		suffix := "-" + strconv.Itoa(i)
		cand := clip(base, MaxNameLen-len(suffix)) + suffix
		if !isTaken(cand) {
			return cand
		}
	}
}

// suggestBase is SuggestName without the clash handling.
func suggestBase(email string) string {
	local, domain, ok := strings.Cut(email, "@")
	if !ok {
		return sanitizeName(email)
	}
	if strings.HasSuffix(domain, "noreply.github.com") {
		// 12345+login@users.noreply.github.com
		if _, login, has := strings.Cut(local, "+"); has {
			return sanitizeName(login)
		}
		return sanitizeName(local)
	}
	if webmail(domain) {
		user, tag, has := strings.Cut(local, "+")
		if has {
			if n := sanitizeName(tag); n != "" {
				return n
			}
		}
		if n := sanitizeName(user); n != "" {
			return n
		}
		return "personal"
	}
	labels := strings.Split(domain, ".")
	if len(labels) > 1 {
		labels = labels[:len(labels)-1] // the top-level domain
	}
	if n := len(labels); n > 1 {
		switch labels[n-1] {
		case "co", "com", "org", "net", "ac", "gov", "edu", "ltd", "plc":
			labels = labels[:n-1] // acme.co.uk
		}
	}
	if n := sanitizeName(labels[len(labels)-1]); n != "" {
		return n
	}
	return sanitizeName(local)
}

// sanitizeName turns any text into a valid name, or "" if nothing is left:
// anything that is not a letter or digit becomes one dash, dashes do not
// repeat or sit at either end, and the result is at most MaxNameLen long.
func sanitizeName(s string) string {
	var b strings.Builder
	dash := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteByte(c)
			continue
		}
		dash = true
	}
	out := clip(b.String(), MaxNameLen)
	if IsDefault(out) {
		return "default-account"
	}
	return out
}

// clip shortens a sanitized name to n bytes without leaving a dash at the end.
func clip(s string, n int) string {
	if len(s) > n {
		s = s[:n]
	}
	return strings.TrimRight(s, "-")
}
