package shims

import (
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
	"strings"
)

// The user PATH is edited as text, never re-built from parsed parts: every
// entry other than the shim folder keeps its exact spelling, its %VAR%s,
// its empty segments and its trailing semicolon.

// sameEntry reports whether one PATH entry names dir, after expanding %VAR%
// and ignoring case, quotes and a trailing backslash.
func sameEntry(entry, dir string, getenv func(string) string) bool {
	e := strings.TrimSpace(strings.Trim(strings.TrimSpace(entry), `"`))
	if e == "" {
		return false
	}
	e = expandPercent(e, getenv)
	return strings.EqualFold(strings.TrimRight(filepath.Clean(e), `\/`), strings.TrimRight(filepath.Clean(dir), `\/`))
}

// HasEntry reports whether the PATH value lists dir.
func HasEntry(value, dir string, getenv func(string) string) bool {
	for _, e := range strings.Split(value, ";") {
		if sameEntry(e, dir, getenv) {
			return true
		}
	}
	return false
}

// PrependEntry puts dir first in a PATH value. Any other spelling of dir
// further along is removed; everything else is kept byte for byte. changed
// is false when dir already comes first.
func PrependEntry(value, dir string, getenv func(string) string) (string, bool) {
	parts := strings.Split(value, ";")
	if len(parts) > 0 && sameEntry(parts[0], dir, getenv) {
		rest, _ := RemoveEntry(strings.Join(parts[1:], ";"), dir, getenv)
		out := parts[0]
		if len(parts) > 1 {
			out += ";" + rest
		}
		return out, out != value
	}
	rest, _ := RemoveEntry(value, dir, getenv)
	if rest == "" {
		return dir, true
	}
	return dir + ";" + rest, true
}

// RemoveEntry removes every spelling of dir from a PATH value and keeps
// everything else byte for byte.
func RemoveEntry(value, dir string, getenv func(string) string) (string, bool) {
	parts := strings.Split(value, ";")
	out := parts[:0:0]
	changed := false
	for _, p := range parts {
		if sameEntry(p, dir, getenv) {
			changed = true
			continue
		}
		out = append(out, p)
	}
	return strings.Join(out, ";"), changed
}

// expandPercent expands %NAME% the way Windows expands REG_EXPAND_SZ; an
// unknown name is left as it is.
func expandPercent(s string, getenv func(string) string) string {
	if getenv == nil || !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '%')
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		j := strings.IndexByte(s[i+1:], '%')
		if j < 0 {
			b.WriteString(s)
			return b.String()
		}
		name := s[i+1 : i+1+j]
		b.WriteString(s[:i])
		if v := getenv(name); name != "" && v != "" {
			b.WriteString(v)
		} else {
			b.WriteString(s[i : i+j+2])
		}
		s = s[i+j+2:]
	}
}

func randHex() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
