package restable

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/scan"
)

// The table is a tree at most three levels deep: repository, project, item.
//
// A repository earns a heading of its own only when it has something to
// gather: two or more projects, or one project that is not the repository's
// own root folder. A repository that is one project at its root is drawn
// exactly as a plain project always was, so a folder of ordinary repos looks
// the way it did before nesting existed. Everything with no repository around
// it is a top-level project heading.
//
// Top-level headings are labelled with the shortest trailing part of their
// path that no other heading shares, the way an editor labels two tabs both
// called index.ts. Before that rule a monorepo scanned from D:\ showed "web",
// "web", "admin", "admin" as four unrelated headings.

// Heading key prefixes. A repository and the project at its root share a
// path, and their fold states must not, so each kind of key says which it is.
const (
	repoKeyPrefix    = "r:"
	projectKeyPrefix = "p:"
)

// rootLabel is the heading of the project that is the repository's own root
// folder, drawn under the repository heading beside apps/web and the rest.
// The repository's name would repeat the heading directly above it and read
// like a folder of the same name nested inside; "(root)" says what it is,
// and sorts ahead of the sub-folders under a name sort.
const rootLabel = "(root)"

// label is a heading's text in two parts: dir is the leading context drawn
// muted, base is the last path segment drawn in the heading style. Keeping
// them apart lets the eye find "admin" while "apps/" stays quiet.
type label struct {
	dir  string
	base string
}

// String is the label as one plain string, which is what a name sort
// compares.
func (l label) String() string { return l.dir + l.base }

// treeShape is what the table works out from every item it holds, filtered
// or not: which repositories get a heading and what each top-level heading
// is called. It is computed from the whole set rather than the filtered one
// so that typing a filter never re-labels or re-nests what is left on
// screen, and it is recomputed only when items arrive, never per keystroke.
type treeShape struct {
	// nests holds the lower-cased path of every repository that gets its
	// own heading.
	nests map[string]bool
	// labels maps a top-level heading key to its label.
	labels map[string]label
}

// shapeOf works out the tree shape for items.
func shapeOf(items []scan.Item) treeShape {
	projects := map[string]map[string]struct{}{}
	for i := range items {
		rk := repoKey(items[i])
		if rk == "" {
			continue
		}
		set, ok := projects[rk]
		if !ok {
			set = map[string]struct{}{}
			projects[rk] = set
		}
		set[projectKey(items[i])] = struct{}{}
	}

	nests := make(map[string]bool, len(projects))
	for rk, set := range projects {
		if len(set) >= 2 {
			nests[rk] = true
			continue
		}
		for pk := range set {
			nests[rk] = pk != rk
		}
	}

	s := treeShape{nests: nests}
	paths := map[string]string{}
	for i := range items {
		k, p := s.top(items[i])
		if _, ok := paths[k]; !ok {
			paths[k] = p
		}
	}
	s.labels = disambiguate(paths)
	return s
}

// top returns the key and the path of the top-level heading an item sits
// under: its repository when that nests, its project otherwise.
func (s treeShape) top(it scan.Item) (key, path string) {
	if rk := repoKey(it); rk != "" && s.nests[rk] {
		return repoKeyPrefix + rk, trimSep(it.Repo)
	}
	return projectKeyPrefix + projectKey(it), projectPath(it)
}

// nested reports whether an item sits under a repository heading, with its
// project as a second level.
func (s treeShape) nested(it scan.Item) bool {
	rk := repoKey(it)
	return rk != "" && s.nests[rk]
}

// labelFor is the label of a top-level heading. A key the shape has never
// seen, which only a table built without [shapeOf] could ask for, falls back
// to the last path segment.
func (s treeShape) labelFor(key, path string) label {
	if l, ok := s.labels[key]; ok {
		return l
	}
	return label{base: baseOrPath(path)}
}

// projectPath is the directory an item is grouped under.
func projectPath(it scan.Item) string {
	p := it.Project
	if p == "" {
		p = parentPath(it.Path)
	}
	return trimSep(p)
}

// projectKey is the identity of the project an item belongs under, compared
// without regard to case because Windows paths are case-insensitive.
func projectKey(it scan.Item) string { return strings.ToLower(projectPath(it)) }

// repoKey is the identity of the repository an item belongs to, or "".
func repoKey(it scan.Item) string { return strings.ToLower(trimSep(it.Repo)) }

// relLabel labels a project under its repository heading by its path inside
// the repository, with forward slashes whatever the platform, since that is
// how a monorepo's own tooling and README name its packages.
func relLabel(repo, project string) label {
	repo, project = trimSep(repo), trimSep(project)
	if strings.EqualFold(repo, project) {
		return label{base: rootLabel}
	}
	if len(project) > len(repo) && strings.EqualFold(project[:len(repo)], repo) &&
		(project[len(repo)] == '\\' || project[len(repo)] == '/') {
		segs := splitSegs(project[len(repo)+1:])
		if len(segs) > 0 {
			return splitLabel(strings.Join(segs, "/"), "/")
		}
	}
	return label{base: baseOrPath(project)}
}

// disambiguate labels every top-level heading with the shortest suffix of
// its path that no other heading shares.
//
// It works in rounds by suffix length. In round k every path contributes its
// last k segments to a count, and a path still unlabelled whose k-suffix
// occurs once takes it as its label. A path that runs out of segments while
// still ambiguous, which happens only when two paths differ by volume alone
// or one is a suffix of the other, goes through the same rounds again with
// its volume in front: D:\…\web beside E:\…\web. Each round is a single pass
// with a map, so a scan of a whole drive with a thousand projects stays
// linear in the number of headings rather than quadratic.
func disambiguate(paths map[string]string) map[string]label {
	type entry struct {
		key  string
		vol  string
		sep  string
		segs []string
	}
	entries := make([]entry, 0, len(paths))
	longest := 0
	for k, p := range paths {
		vol, rest := splitVolume(p)
		sep := "/"
		if strings.Contains(p, `\`) {
			sep = `\`
		}
		e := entry{key: k, vol: vol, sep: sep, segs: splitSegs(rest)}
		entries = append(entries, e)
		longest = max(longest, len(e.segs))
	}

	suffix := func(e entry, k int) string {
		return strings.ToLower(strings.Join(e.segs[len(e.segs)-k:], "/"))
	}

	out := make(map[string]label, len(entries))
	pending := make([]int, 0, len(entries))
	for i, e := range entries {
		if len(e.segs) == 0 {
			// A volume root, D:\ itself, has no segment to shorten to.
			out[e.key] = label{base: paths[e.key]}
			continue
		}
		pending = append(pending, i)
	}

	// Plain suffixes first.
	for k := 1; k <= longest && len(pending) > 0; k++ {
		counts := map[string]int{}
		for _, e := range entries {
			if len(e.segs) >= k {
				counts[suffix(e, k)]++
			}
		}
		next := pending[:0]
		for _, i := range pending {
			e := entries[i]
			if len(e.segs) >= k && counts[suffix(e, k)] == 1 {
				out[e.key] = splitLabel(strings.Join(e.segs[len(e.segs)-k:], "/"), "/")
				continue
			}
			next = append(next, i)
		}
		pending = next
	}

	// Whatever is left needs its volume to tell it apart.
	for k := 1; k <= longest && len(pending) > 0; k++ {
		counts := map[string]int{}
		for _, e := range entries {
			if len(e.segs) >= k {
				counts[strings.ToLower(e.vol)+"|"+suffix(e, k)]++
			}
		}
		next := pending[:0]
		for _, i := range pending {
			e := entries[i]
			unique := len(e.segs) >= k && counts[strings.ToLower(e.vol)+"|"+suffix(e, k)] == 1
			switch {
			case len(e.segs) == k:
				// Out of segments: the whole path is the only honest label.
				out[e.key] = splitLabel(e.vol+e.sep+strings.Join(e.segs, e.sep), e.sep)
			case unique:
				out[e.key] = splitLabel(e.vol+e.sep+"…"+e.sep+strings.Join(e.segs[len(e.segs)-k:], e.sep), e.sep)
			default:
				next = append(next, i)
				continue
			}
		}
		pending = next
	}
	return out
}

// splitLabel cuts a label at its last separator: everything up to and
// including it is the muted context, the rest is the name.
func splitLabel(s, sep string) label {
	if i := strings.LastIndex(s, sep); i >= 0 {
		return label{dir: s[:i+len(sep)], base: s[i+len(sep):]}
	}
	return label{base: s}
}

// splitVolume separates a Windows volume from the rest of a path: a drive
// letter ("D:") or a UNC share ("\\server\share"). It is written out rather
// than taken from filepath.VolumeName so the table labels Windows paths the
// same when its tests run on Linux.
func splitVolume(p string) (vol, rest string) {
	if len(p) >= 2 && p[1] == ':' && isLetter(p[0]) {
		return p[:2], p[2:]
	}
	if len(p) >= 2 && isSep(p[0]) && isSep(p[1]) {
		segs := splitSegs(p)
		if len(segs) >= 2 {
			vol = p[:2] + segs[0] + p[1:2] + segs[1]
			return vol, p[len(vol):]
		}
	}
	return "", p
}

// splitSegs splits a path into its non-empty segments on either separator.
func splitSegs(p string) []string {
	return strings.FieldsFunc(p, func(r rune) bool { return r == '\\' || r == '/' })
}

// trimSep drops trailing separators, so "D:\work\" and "D:\work" are one
// path. A bare volume root keeps its separator, since "D:" alone means the
// current directory on that drive, not its root.
func trimSep(p string) string {
	t := strings.TrimRight(p, `\/`)
	if t == "" || (len(t) == 2 && t[1] == ':') {
		return p
	}
	return t
}

// baseOrPath is the last segment of p, or p itself when it has none.
func baseOrPath(p string) string {
	if b := baseName(p); b != "" {
		return b
	}
	return p
}

// isLetter reports an ASCII letter, the only thing a drive can be named.
func isLetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

// isSep reports either path separator.
func isSep(c byte) bool { return c == '\\' || c == '/' }

// fitLabel squeezes a label into exactly w cells. The name is what the user
// is looking for, so the context gives way first, from its left end: a deep
// path keeps "…ages/admin" rather than "packages/ad…".
func fitLabel(l label, w int) label {
	if w <= 0 {
		return label{}
	}
	dw, bw := ansi.StringWidth(l.dir), ansi.StringWidth(l.base)
	switch {
	case dw+bw <= w:
		return label{dir: l.dir, base: l.base + pad(w-dw-bw)}
	case bw >= w-1:
		return label{base: padRight(l.base, w)}
	default:
		room := w - bw
		return label{dir: ansi.TruncateLeft(l.dir, dw-room+1, "…"), base: l.base}
	}
}
