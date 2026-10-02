// Package whatsnew is the card a person sees once after Devpit was updated:
// what is new in one or two lines, where things moved, an offer to set up
// the AI agent skill when Claude Code is here and the skill is missing or
// older, and the link to the full notes.
//
// The decision is in [Due] and the text in [Releases]: one table, one entry
// per minor release (and, rarely, a patch release that earns a card of its
// own), so a test can require an entry for the version being built.
//
// The card is never shown on a fresh install (first run covers it), never
// for a development build or a version that is not semver, never after a
// downgrade, and never by a command line run: only the TUI's root model
// asks [Due].
package whatsnew

import (
	"strconv"
	"strings"
)

// PageURL is the What's new page on the website.
const PageURL = "https://devpit.zubyr.dev/docs/whats-new"

// Move is one thing that now lives somewhere else.
type Move struct {
	// From is where it was, To where it is now.
	From, To string
}

// Entry is one release's card.
type Entry struct {
	// Version is "0.4" for a minor release, or "0.4.2" for a patch release
	// that has its own card. Every other patch release shows nothing.
	Version string
	// Headline is the first line: the one thing worth knowing.
	Headline string
	// Lines say a little more, one or two sentences.
	Lines []string
	// Moves are the things a returning user will look for in the old place.
	Moves []Move
}

// Releases is the text of every card, newest first. It is a function rather
// than a package variable so nothing can change the table at run time.
func Releases() []Entry {
	return []Entry{
		{
			Version:  "0.4",
			Headline: "Accounts is new.",
			Lines: []string{
				"Tell Devpit once which account a folder uses, and claude, git, gh, vercel, firebase, supabase and wrangler use it in that folder by themselves.",
			},
			Moves: []Move{
				{"Git & SSH", "Accounts › Git"},
				{"Fix stuck ports, Network tools", "Ports & Network"},
				{"Install, Update", "Install & Update"},
				{"Keys 1–8", "keys 1–6, one per section"},
			},
		},
	}
}

// semver is a parsed release: major, minor, patch.
type semver [3]int

// parse reads "0.4.1" or "v0.4.1", with any pre-release or build suffix
// dropped. Anything else ("dev", "", "0.4", "nightly") is not a release.
func parse(v string) (semver, bool) {
	var out semver
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// compare is -1, 0 or 1 as a is older than, the same as or newer than b.
func (a semver) compare(b semver) int {
	for i := range a {
		switch {
		case a[i] < b[i]:
			return -1
		case a[i] > b[i]:
			return 1
		}
	}
	return 0
}

// minor is "M.m".
func (a semver) minor() string { return strconv.Itoa(a[0]) + "." + strconv.Itoa(a[1]) }

// full is "M.m.p".
func (a semver) full() string { return a.minor() + "." + strconv.Itoa(a[2]) }

// IsRelease reports whether v is a real release version (semver, not "dev").
func IsRelease(v string) bool {
	_, ok := parse(v)
	return ok
}

// For returns the card for a running version: the patch release's own entry
// when it has one, else its minor release's.
func For(running string) (Entry, bool) {
	cur, ok := parse(running)
	if !ok {
		return Entry{}, false
	}
	if e, ok := lookup(cur.full()); ok {
		return e, true
	}
	return lookup(cur.minor())
}

// lookup finds the entry with exactly this Version.
func lookup(v string) (Entry, bool) {
	for _, e := range Releases() {
		if e.Version == v {
			return e, true
		}
	}
	return Entry{}, false
}

// Due decides whether the card is shown before home, and which one.
//
//   - First run not finished: no, first run explains everything.
//   - Running version not a release (dev, empty, not semver): no.
//   - Nothing recorded yet, or something unreadable: the config predates
//     the field, so this is an update from an older Devpit: yes.
//   - Recorded version the same or newer (a downgrade): no.
//   - Only the patch number moved: only when that patch has its own entry.
//   - A newer minor or major: yes, its minor release's card.
func Due(running, lastSeen string, firstRunDone bool) (Entry, bool) {
	if !firstRunDone {
		return Entry{}, false
	}
	cur, ok := parse(running)
	if !ok {
		return Entry{}, false
	}
	last, ok := parse(lastSeen)
	if !ok {
		return For(running)
	}
	if last.compare(cur) >= 0 {
		return Entry{}, false
	}
	if last[0] == cur[0] && last[1] == cur[1] {
		return lookup(cur.full())
	}
	return For(running)
}

// Record is the value to store as last seen once the card was dismissed or
// first run finished: the running version, unless it is not a release or
// would move the value backwards (a downgrade never rewrites it).
func Record(lastSeen, running string) string {
	cur, ok := parse(running)
	if !ok {
		return lastSeen
	}
	if last, ok := parse(lastSeen); ok && last.compare(cur) >= 0 {
		return lastSeen
	}
	return strings.TrimPrefix(strings.TrimSpace(running), "v")
}
