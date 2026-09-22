package scan

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Rule describes one kind of reclaimable directory or file.
//
// A rule matches in one of three ways, checked in this order:
//
//   - Names: the directory's base name equals one of these, compared without
//     regard to case, because Windows paths are case-insensitive and "Dist"
//     and "dist" are the same folder.
//   - Match: an arbitrary predicate over the base name, for the handful of
//     rules a name list cannot express.
//   - Locations: absolute paths, written with %ENVVAR% placeholders, that are
//     probed directly instead of being discovered by walking. Package caches
//     and Windows temp folders live at known addresses, so there is no point
//     in hunting for them.
//
// A match is only junk when one of Markers sits beside the matched directory,
// in its parent. That is the single rule that stops Devpit deleting a source
// folder that happens to be called "build". A marker may be a glob, so the
// .NET rule can ask for "*.csproj".
type Rule struct {
	// Name identifies the rule in the UI and on the Item.
	Name string
	// Kind is the category items from this rule belong to.
	Kind Kind
	// Names are the base names this rule matches, compared case-insensitively.
	Names []string
	// Match is an optional extra predicate over the lower-cased base name.
	Match func(lowerName string) bool
	// Locations are absolute paths written with %ENVVAR% placeholders. A rule
	// with Locations is probed directly rather than found by walking.
	Locations []string
	// Markers are file or glob names; any one of them sitting in the matched
	// directory's parent proves the match is real.
	Markers []string
	// MarkersInside asks for the markers inside the matched directory rather
	// than beside it. Unity's Library/Temp pair is the reason this exists.
	MarkersInside bool
	// AllowUnverified lists the match even when no marker was found, flagging
	// it as unverified. Only node_modules uses it: a dependency folder with
	// no package.json beside it is still almost certainly junk, but it is
	// never pre-ticked.
	AllowUnverified bool
	// MinSize, when non-zero, turns the rule into a file rule: regular files
	// at least this large match, directories do not.
	MinSize uint64
	// Tier is how much care deleting a match needs.
	Tier Tier
	// RestoreHint says, in the user's words, how to get the item back. It is
	// never empty.
	RestoreHint string
	// Description is the one-line explanation shown beside the rule.
	Description string
}

// Errors reported by rule validation and by Verify.
var (
	errEmptyCommandExe  = errors.New("scan: command has no executable")
	errEmptyRestoreHint = errors.New("scan: restore hint is empty")

	// ErrGone means the path no longer exists.
	ErrGone = errors.New("scan: the path is no longer there")
	// ErrNotADirectory means the path exists but is not the kind of thing the
	// item said it was.
	ErrNotADirectory = errors.New("scan: the path is not a directory any more")
	// ErrReparsePoint means the path, or something above it, is a junction,
	// symlink or other reparse point.
	ErrReparsePoint = errors.New("scan: the path resolves through a reparse point")
	// ErrRenamed means the base name changed since the scan.
	ErrRenamed = errors.New("scan: the path was renamed since the scan")
	// ErrMarkerMissing means the marker file that made the match junk is gone.
	ErrMarkerMissing = errors.New("scan: the marker file is no longer beside the path")
	// ErrProtectedPath means the path is a drive root, a system location or a
	// network path, none of which Devpit ever deletes.
	ErrProtectedPath = errors.New("scan: the path is protected and is never deleted")
)

// ForbiddenArgError reports a command that carries an argument its own rule
// forbids, such as docker's --volumes.
type ForbiddenArgError struct {
	// Command is the step's display name.
	Command string
	// Arg is the offending argument.
	Arg string
}

// Error implements error.
func (e *ForbiddenArgError) Error() string {
	return fmt.Sprintf("scan: %s must never be run with %s", e.Command, e.Arg)
}

// Validate reports whether a rule is usable: it needs a name, a way to match
// and a restore hint.
func (r Rule) Validate() error {
	if r.Name == "" {
		return errors.New("scan: rule has no name")
	}
	if r.RestoreHint == "" {
		return fmt.Errorf("scan: rule %q: %w", r.Name, errEmptyRestoreHint)
	}
	if len(r.Names) == 0 && r.Match == nil && len(r.Locations) == 0 && r.MinSize == 0 {
		return fmt.Errorf("scan: rule %q matches nothing", r.Name)
	}
	return nil
}

// IsFileRule reports whether the rule matches files rather than directories.
func (r Rule) IsFileRule() bool { return r.MinSize > 0 }

// IsLocationRule reports whether the rule is probed at fixed addresses rather
// than discovered by walking.
func (r Rule) IsLocationRule() bool { return len(r.Locations) > 0 }

// Matches reports whether a base name matches the rule. The comparison is
// case-insensitive: name may be given in any case.
func (r Rule) Matches(name string) bool {
	lower := strings.ToLower(name)
	for _, n := range r.Names {
		if strings.ToLower(n) == lower {
			return true
		}
	}
	if r.Match != nil {
		return r.Match(lower)
	}
	return false
}

// markerDir returns the directory the rule's markers are looked for in.
func (r Rule) markerDir(path string) string {
	if r.MarkersInside {
		return path
	}
	return filepath.Dir(path)
}

// HasMarker reports whether one of the rule's markers sits beside path (or
// inside it, for MarkersInside rules). A rule with no markers always has one,
// because there was nothing to prove.
func (r Rule) HasMarker(path string) bool {
	if len(r.Markers) == 0 {
		return true
	}
	dir := r.markerDir(path)
	for _, marker := range r.Markers {
		if strings.ContainsAny(marker, "*?[") {
			matches, err := filepath.Glob(filepath.Join(dir, marker))
			if err == nil && len(matches) > 0 {
				return true
			}
			continue
		}
		if _, err := os.Lstat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
}

// ExpandLocations returns the rule's locations with their %ENVVAR%
// placeholders replaced from the environment. A location whose variables are
// unset is dropped rather than half-expanded, so a scan never walks a path
// like "\npm-cache".
func (r Rule) ExpandLocations() []string {
	out := make([]string, 0, len(r.Locations))
	for _, loc := range r.Locations {
		expanded, ok := expandEnvPlaceholders(loc)
		if !ok {
			continue
		}
		out = append(out, filepath.Clean(expanded))
	}
	return out
}

// resolveLocations expands a location rule's placeholders and then its globs
// against the filesystem, so a rule can say
// "%LOCALAPPDATA%\JetBrains\*\caches" without knowing which products are
// installed. A pattern that matches nothing simply contributes nothing.
func resolveLocations(r *Rule) []string {
	expanded := r.ExpandLocations()
	out := make([]string, 0, len(expanded))
	for _, loc := range expanded {
		if !strings.ContainsAny(loc, "*?[") {
			out = append(out, loc)
			continue
		}
		matches, err := filepath.Glob(loc)
		if err != nil {
			continue
		}
		out = append(out, matches...)
	}
	return out
}

// expandEnvPlaceholders replaces every %NAME% in s with the environment
// variable NAME. It reports false when any placeholder is unset or empty.
func expandEnvPlaceholders(s string) (string, bool) {
	var b strings.Builder
	for {
		start := strings.IndexByte(s, '%')
		if start < 0 {
			b.WriteString(s)
			return b.String(), true
		}
		end := strings.IndexByte(s[start+1:], '%')
		if end < 0 {
			b.WriteString(s)
			return b.String(), true
		}
		end += start + 1
		name := s[start+1 : end]
		value := os.Getenv(name)
		if value == "" {
			return "", false
		}
		b.WriteString(s[:start])
		b.WriteString(value)
		s = s[end+1:]
	}
}

// ruleTable is the prepared form of Options.Rules: an exact-name index for
// the common case plus the rules that need a predicate or a file size.
type ruleTable struct {
	byName map[string][]*Rule
	preds  []*Rule
	files  []*Rule
	locs   []*Rule
}

// newRuleTable prepares rules for matching. Invalid rules are dropped rather
// than failing the scan, because a broken rule should not stop a user
// reclaiming space; Validate is what the rule-table test calls.
func newRuleTable(rules []Rule) *ruleTable {
	t := &ruleTable{byName: make(map[string][]*Rule, len(rules))}
	for i := range rules {
		r := &rules[i]
		if r.Validate() != nil {
			continue
		}
		switch {
		case r.IsLocationRule():
			t.locs = append(t.locs, r)
		case r.IsFileRule():
			t.files = append(t.files, r)
		}
		for _, n := range r.Names {
			lower := strings.ToLower(n)
			t.byName[lower] = append(t.byName[lower], r)
		}
		if r.Match != nil {
			t.preds = append(t.preds, r)
		}
	}
	return t
}

// matchDir returns the first rule matching a directory's lower-cased base
// name, preferring a rule whose marker is present over one whose is not.
func (t *ruleTable) matchDir(lowerName, path string) (*Rule, bool) {
	var fallback *Rule
	consider := func(r *Rule) bool {
		if r.IsFileRule() || r.IsLocationRule() {
			return false
		}
		if r.HasMarker(path) {
			return true
		}
		if fallback == nil && r.AllowUnverified {
			fallback = r
		}
		return false
	}
	for _, r := range t.byName[lowerName] {
		if consider(r) {
			return r, true
		}
	}
	for _, r := range t.preds {
		if !r.Matches(lowerName) {
			continue
		}
		if consider(r) {
			return r, true
		}
	}
	if fallback != nil {
		return fallback, false
	}
	return nil, false
}

// matchFile returns the first file rule a regular file of the given size
// satisfies.
func (t *ruleTable) matchFile(lowerName string, size uint64) (*Rule, bool) {
	for _, r := range t.files {
		if size < r.MinSize {
			continue
		}
		if len(r.Names) == 0 && r.Match == nil {
			return r, true
		}
		if r.Matches(lowerName) {
			return r, true
		}
	}
	return nil, false
}
