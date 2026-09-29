package scan

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// repoCache answers "which repository owns this project?" for the length of
// one scan, which is what [Item.Repo] records.
//
// The answer is the nearest directory at or above the project, and at or
// below the scan root, that holds a .git entry. A directory and a file both
// count: a worktree and a submodule check out with a .git file that points
// elsewhere, and a submodule is still its own repository to the person
// reading the results. The innermost one wins for the same reason.
//
// The walk never climbs above the root. A scan of D:\work\repo\apps is a
// request to look at apps, and reporting a repository the user never pointed
// at would group the results under a folder that is not on screen anywhere.
//
// Every directory the climb passes through is remembered, so the first
// project in a monorepo costs a handful of Lstat calls and every sibling
// after it costs a map read. The sizer pool measures in parallel, hence the
// mutex; the Lstat calls themselves run outside it, and two workers racing to
// the same directory only repeat a stat, never disagree.
type repoCache struct {
	mu   sync.Mutex
	seen map[repoKey]string
}

// repoKey is one directory's answer under one root. The root is part of the
// key because the same folder can have a repository under one root and none
// under a narrower one that stops the climb before it gets there.
type repoKey struct {
	root string
	dir  string
}

// newRepoCache returns an empty cache.
func newRepoCache() *repoCache {
	return &repoCache{seen: make(map[repoKey]string)}
}

// lookup returns the repository that owns dir under root, or "" when no .git
// entry sits between them. A dir outside root has no repository by
// definition, which is what a location rule's fixed path gets.
func (c *repoCache) lookup(root, dir string) string {
	if root == "" || dir == "" {
		return ""
	}
	root, dir = normalizePath(root), normalizePath(dir)
	if !underPath(dir, root) {
		return ""
	}

	var climbed []string
	repo := ""
	for d := dir; ; {
		if got, ok := c.get(root, d); ok {
			repo = got
			break
		}
		climbed = append(climbed, d)
		if hasGitEntry(d) {
			repo = d
			break
		}
		parent := filepath.Dir(d)
		if strings.EqualFold(d, root) || parent == d {
			break
		}
		d = parent
	}

	// Every directory passed on the way up had no .git of its own, so it
	// belongs to whatever the climb ended on: the repository it found, the
	// cached answer it reached, or none.
	c.mu.Lock()
	for _, d := range climbed {
		c.seen[repoKey{root: strings.ToLower(root), dir: strings.ToLower(d)}] = repo
	}
	c.mu.Unlock()
	return repo
}

// get reads one cached answer. Keys are lower-cased because Windows paths are
// case-insensitive and a project can be reached through either spelling.
func (c *repoCache) get(root, dir string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	got, ok := c.seen[repoKey{root: strings.ToLower(root), dir: strings.ToLower(dir)}]
	return got, ok
}

// hasGitEntry reports whether dir holds a .git directory or file. It uses
// Lstat, so a .git that is itself a link is recognised without being
// followed, and it never reads the entry: existence is the whole question.
func hasGitEntry(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}
