package foldertree

import (
	"sort"
	"strings"
)

// Build turns a flat list of ruled folders into the tree [New] draws.
//
// A ruled folder is always a node. A folder without rules becomes one only
// where two or more ruled folders meet under it, so the tree has exactly the
// joints it needs. A drive is never a node on its own unless it has rules:
// everything already hangs from Everywhere, so C:\ would only add a level.
// Paths are compared the way Windows does, ignoring case and treating / and
// \ alike; each node keeps the spelling of the path it was given. Siblings
// are sorted by name.
func Build(folders []Folder) []Node {
	root := &trie{kids: map[string]*trie{}}
	for i := range folders {
		t := root
		for _, seg := range segments(folders[i].Path) {
			k := normPath(seg.name)
			next, ok := t.kids[k]
			if !ok {
				next = &trie{name: seg.name, path: seg.path, kids: map[string]*trie{}}
				t.kids[k] = next
				t.order = append(t.order, next)
			}
			t = next
		}
		if t != root {
			t.folder = &folders[i]
		}
	}
	var out []Node
	for _, drive := range root.sorted() {
		out = append(out, drive.nodes(true)...)
	}
	return out
}

// trie is one path segment while [Build] works.
type trie struct {
	name   string
	path   string
	kids   map[string]*trie
	order  []*trie
	folder *Folder
}

// sorted returns the children in name order, ignoring case.
func (t *trie) sorted() []*trie {
	out := append([]*trie(nil), t.order...)
	sort.SliceStable(out, func(a, b int) bool {
		return strings.ToLower(out[a].name) < strings.ToLower(out[b].name)
	})
	return out
}

// nodes turns a segment and everything under it into the nodes that stand
// for it: one node when it has rules or joins two ruled branches, otherwise
// whatever its children became, passed straight up to its parent.
func (t *trie) nodes(drive bool) []Node {
	var kids []Node
	for _, k := range t.sorted() {
		kids = append(kids, k.nodes(false)...)
	}
	if t.folder == nil && (drive || len(kids) < 2) {
		return kids
	}
	n := Node{Path: t.path, Children: kids}
	if t.folder != nil {
		n.Path = t.folder.Path
		n.Chips = t.folder.Chips
		n.State = t.folder.State
		n.Current = t.folder.Current
	}
	return []Node{n}
}

// segment is one step down a path, with the path up to and including it.
type segment struct {
	name string
	path string
}

// segments splits a path into its steps. The first step is the root as it
// is written: "C:\" for a drive, `\\server\share` for a share.
func segments(p string) []segment {
	p = strings.TrimRight(strings.TrimSpace(p), `\/`)
	if p == "" {
		return nil
	}
	var rootEnd int
	if strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, `//`) {
		// \\server\share: skip the server name, then the share name.
		rootEnd = len(p)
		seps := 0
		for i := 2; i < len(p); i++ {
			if isSep(rune(p[i])) {
				if seps++; seps == 2 {
					rootEnd = i
					break
				}
			}
		}
	} else {
		i := strings.IndexAny(p, `\/`)
		if i < 0 {
			return []segment{{name: p, path: p}}
		}
		rootEnd = i + 1
	}
	out := []segment{{name: p[:rootEnd], path: p[:rootEnd]}}
	for pos := rootEnd; pos < len(p); {
		if isSep(rune(p[pos])) {
			pos++
			continue
		}
		end := len(p)
		if j := strings.IndexAny(p[pos:], `\/`); j >= 0 {
			end = pos + j
		}
		out = append(out, segment{name: p[pos:end], path: p[:end]})
		pos = end
	}
	return out
}

// isSep reports whether r separates path segments.
func isSep(r rune) bool { return r == '\\' || r == '/' }

// compact folds every chain of plain connectors in a tree a screen built for
// itself: a node with no rules, no state and a single child is replaced by
// that child. It then fills in every empty Name relative to the node above
// it, so a folded chain reads as one row, `Users\you\code`.
func compact(nodes []Node, parent string) []Node {
	out := make([]Node, 0, len(nodes))
	for _, n := range nodes {
		for n.Name == "" && len(n.Chips) == 0 && n.State == StateOK && !n.Current && len(n.Children) == 1 {
			n = n.Children[0]
		}
		n.Children = compact(n.Children, n.Path)
		if n.Name == "" {
			n.Name = relName(parent, n.Path)
		}
		out = append(out, n)
	}
	return out
}

// relName is path written relative to parent, or the whole path when it is
// not under parent (or there is no parent).
func relName(parent, path string) string {
	if parent == "" || path == "" {
		return path
	}
	lp, lc := normPath(strings.TrimRight(parent, `\/`)), normPath(path)
	if !strings.HasPrefix(lc, lp) || len(path) <= len(lp) || !isSep(rune(path[len(lp)])) {
		return path
	}
	return strings.TrimLeft(path[len(lp):], `\/`)
}
