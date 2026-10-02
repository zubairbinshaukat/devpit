// Package protect is the one list of folders Devpit must never scan as junk,
// size into a deletable item, or delete: login and account folders. Claude
// Code's settings and sign-in, SSH keys, each CLI's saved login, and Devpit's
// own account folders all live in ordinary directories under the user
// profile, and a cleaner that walks the profile would otherwise find a
// node_modules or a large file inside one and offer to remove it.
//
// Clean uses the list today, in the scanner's filters and in the delete
// pre-flight, independently. Accounts adds every account folder it knows at
// runtime with [List.With], including imported ones that live somewhere else.
//
// A [List] is a value. It is built from an environment lookup function rather
// than read from the process at import time, so a test can hand it a fake
// environment, and nothing in this package holds mutable state.
//
// Matching is a string comparison on a cleaned spelling of each path:
// case-insensitive, slash-insensitive, with trailing separators, `\\?\`
// prefixes and relative paths handled, and 8.3 short names expanded on
// Windows when a path has one. Each protected folder that exists is also
// resolved through any junction or link once, when the list is built, so a
// `.claude` that is a link to another drive protects that drive's folder too.
package protect

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// LookupEnv is the shape of [os.LookupEnv]. [Default] reads the environment
// only through it.
type LookupEnv func(key string) (string, bool)

// Entry is one protected location.
type Entry struct {
	// Path is the cleaned absolute path.
	Path string
	// What says, in a user's words, what lives there: "your SSH keys".
	What string

	// key is Path lower-cased, the form every comparison uses.
	key string
}

// List is a set of protected locations. The zero value protects nothing;
// [Default] is where a real list starts. Every method returns a new List and
// never changes the one it was called on, so a List can be shared freely.
type List struct {
	entries []Entry
}

// AccountFolder is what [List.With] says lives in a folder it adds.
const AccountFolder = "an account folder Devpit manages"

// rule is one row of the built-in list: a folder relative to the value of an
// environment variable. An empty rel means the variable names the folder.
type rule struct {
	env  string
	rel  string
	what string
}

// defaultRules is the built-in list. Paths are written with forward slashes
// and converted, so the table reads the same on every platform.
func defaultRules() []rule {
	const claude = "Claude Code's settings and sign-in"
	return []rule{
		{"USERPROFILE", ".claude", claude},
		{"USERPROFILE", ".claude.json", "Claude Code's account file"},
		// Claude Code reads its folder from here when it is set, which is how
		// a second Claude account works.
		{"CLAUDE_CONFIG_DIR", "", claude},
		{"USERPROFILE", ".devpit", "Devpit's account folders and Git rules"},
		{"USERPROFILE", ".claude-switch", "saved Claude accounts"},
		{"USERPROFILE", ".ssh", "your SSH keys"},
		{"USERPROFILE", ".convex", "the Convex sign-in"},
		{"USERPROFILE", ".supabase", "the Supabase sign-in"},
		{"USERPROFILE", ".wrangler", "the Cloudflare Wrangler sign-in"},
		{"USERPROFILE", ".config/configstore", "sign-ins saved by Firebase and other npm tools"},
		{"APPDATA", "GitHub CLI", "the GitHub CLI sign-in"},
		{"APPDATA", "xdg.data/com.vercel.cli", "the Vercel sign-in"},
		{"APPDATA", "xdg.config/.wrangler", "the Cloudflare Wrangler sign-in"},
		{"APPDATA", "Anthropic", "Anthropic's app data and sign-in"},
		{"APPDATA", "Claude", "the Claude desktop app's settings and sign-in"},
		{"APPDATA", "devpit", "Devpit's own settings"},
	}
}

// Default builds the built-in list from env. Pass [os.LookupEnv] in
// production and a fake in tests.
//
// A variable that is unset, empty, blank or not an absolute path drops every
// rule built on it. It never turns `%USERPROFILE%\.ssh` into `\.ssh`, a path
// relative to wherever Devpit happens to run, and a variable that names a
// whole drive is dropped too, so no rule can ever become "everything".
func Default(env LookupEnv) List {
	var l List
	if env == nil {
		return l
	}
	for _, r := range defaultRules() {
		base, ok := env(r.env)
		if !ok {
			continue
		}
		base = strings.TrimSpace(base)
		if base == "" {
			continue
		}
		// The variable must be absolute on its own. Making it absolute
		// against the working directory would invent a location.
		if !filepath.IsAbs(filepath.FromSlash(stripDevicePrefix(base))) {
			continue
		}
		p := base
		if r.rel != "" {
			p = filepath.Join(filepath.FromSlash(stripDevicePrefix(base)), filepath.FromSlash(r.rel))
		}
		clean, ok := canon(p)
		if !ok || isRoot(clean) {
			continue
		}
		l.entries = addEntry(l.entries, clean, r.what)
	}
	return l
}

// With returns a copy of l that also protects each of extra, described as an
// account folder. Empty entries are ignored; a relative entry is made
// absolute against the working directory.
func (l List) With(extra ...string) List {
	return l.WithReason(AccountFolder, extra...)
}

// WithReason is [List.With] with the caller's own words for what lives in
// the folders, such as "the work Claude account".
func (l List) WithReason(what string, extra ...string) List {
	out := List{entries: slices.Clone(l.entries)}
	if strings.TrimSpace(what) == "" {
		what = AccountFolder
	}
	for _, p := range extra {
		clean, ok := canon(p)
		if !ok {
			continue
		}
		out.entries = addEntry(out.entries, clean, what)
	}
	return out
}

// Union returns a list protecting everything either list protects.
func (l List) Union(other List) List {
	out := List{entries: slices.Clone(l.entries)}
	for _, e := range other.entries {
		if !out.has(e.key) {
			out.entries = append(out.entries, e)
		}
	}
	return out
}

// Entries returns the protected locations, in the order they were added. The
// slice is a copy.
func (l List) Entries() []Entry {
	return slices.Clone(l.entries)
}

// Len is the number of protected locations, counting a folder reached through
// a link once per spelling.
func (l List) Len() int { return len(l.entries) }

// Inside reports whether path is a protected location, sits inside one, or is
// the `<folder>.lock` file or folder beside one. This is the scanner's
// question: a directory it must never descend into, and a file it must never
// report.
//
// The reason is a clause a sentence can carry: "it is inside
// C:\Users\me\.ssh, which holds your SSH keys".
func (l List) Inside(path string) (bool, string) {
	k, ok := key(path)
	if !ok {
		return false, ""
	}
	for _, e := range l.entries {
		switch {
		case k == e.key:
			return true, "it holds " + e.What
		case under(k, e.key):
			return true, "it is inside " + e.Path + ", which holds " + e.What
		case k == e.key+".lock", under(k, e.key+".lock"):
			return true, "it is the lock beside " + e.Path + ", which holds " + e.What
		}
	}
	return false, ""
}

// WouldRemove reports whether deleting path would take a protected location
// with it: the location is path itself or sits somewhere below it. This is the
// delete side's second question, after [List.Inside]: deleting a user's
// profile folder would remove their `.ssh` with it.
func (l List) WouldRemove(path string) (bool, string) {
	k, ok := key(path)
	if !ok {
		return false, ""
	}
	for _, e := range l.entries {
		switch {
		case k == e.key:
			return true, "it holds " + e.What
		case under(e.key, k):
			return true, "it contains " + e.Path + ", which holds " + e.What
		}
	}
	return false, ""
}

// Covers reports whether path is refused in either direction: it is inside a
// protected location ([List.Inside]) or deleting it would remove one
// ([List.WouldRemove]). The delete pre-flight asks this.
func (l List) Covers(path string) (bool, string) {
	if ok, why := l.Inside(path); ok {
		return true, why
	}
	return l.WouldRemove(path)
}

// addEntry appends clean to entries, and, when it exists and resolves somewhere
// else through a junction, a link or a short name, that resolved spelling
// too. Duplicates are dropped.
func addEntry(entries []Entry, clean, what string) []Entry {
	spellings := []string{clean}
	if final, ok := finalPath(clean); ok {
		if fc, ok := canon(final); ok && !isRoot(fc) {
			spellings = append(spellings, fc)
		}
	}
	for _, s := range spellings {
		k := strings.ToLower(s)
		if slices.ContainsFunc(entries, func(e Entry) bool { return e.key == k }) {
			continue
		}
		entries = append(entries, Entry{Path: s, What: what, key: k})
	}
	return entries
}

// has reports whether the list already holds a location with this key.
func (l List) has(k string) bool {
	return slices.ContainsFunc(l.entries, func(e Entry) bool { return e.key == k })
}

// key is the comparison form of a path: cleaned and lower-cased.
func key(path string) (string, bool) {
	c, ok := canon(path)
	if !ok {
		return "", false
	}
	return strings.ToLower(c), true
}

// under reports whether child sits strictly below parent. Both are keys. The
// test is component-aware, so C:\foobar is not under C:\foo.
func under(child, parent string) bool {
	if parent == "" || len(child) <= len(parent) {
		return false
	}
	prefix := parent
	if !strings.HasSuffix(prefix, string(os.PathSeparator)) {
		prefix += string(os.PathSeparator)
	}
	return strings.HasPrefix(child, prefix)
}

// isRoot reports whether a cleaned absolute path is a drive, share or
// filesystem root.
func isRoot(p string) bool {
	vol := filepath.VolumeName(p)
	rest := strings.Trim(p[len(vol):], `\/`)
	return rest == ""
}
