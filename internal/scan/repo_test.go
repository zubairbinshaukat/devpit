package scan_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// nodeProject writes a Node project with a node_modules the walker will list,
// at dir.
func nodeProject(t *testing.T, dir string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "package.json"), 40)
	writeFile(t, filepath.Join(dir, "node_modules", "dep", "index.js"), 500)
}

// repoOf scans root and returns the Repo of the item whose path ends in
// relSuffix, relative to root with forward slashes, or "" for none.
func repoOf(t *testing.T, root, relSuffix string) string {
	t.Helper()
	items, _, err := runScan(t, projectOptions(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	it, ok := findItem(items, relSuffix)
	if !ok {
		t.Fatalf("%s was not listed; got %v", relSuffix, relPaths(t, root, items))
	}
	if it.Repo == "" {
		return ""
	}
	rel, err := filepath.Rel(root, it.Repo)
	if err != nil {
		t.Fatalf("Repo %q is not relative to the root %q: %v", it.Repo, root, err)
	}
	return filepath.ToSlash(rel)
}

// The repository is found through a .git directory, and every project in a
// monorepo reports the same one: that is what lets the table nest them.
func TestRepoFoundThroughAGitDirectory(t *testing.T) {
	root := t.TempDir()
	mkdir(t, filepath.Join(root, "yard", ".git"))
	nodeProject(t, filepath.Join(root, "yard", "apps", "web"))
	nodeProject(t, filepath.Join(root, "yard", "apps", "admin"))

	for _, p := range []string{"apps/web/node_modules", "apps/admin/node_modules"} {
		if got := repoOf(t, root, p); got != "yard" {
			t.Errorf("%s: Repo = %q, want yard", p, got)
		}
	}
}

// A worktree checks out with a .git file rather than a directory, and it is
// still a repository.
func TestRepoFoundThroughAGitFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "wt", ".git"), 20)
	nodeProject(t, filepath.Join(root, "wt"))

	if got := repoOf(t, root, "wt/node_modules"); got != "wt" {
		t.Errorf("Repo = %q, want wt", got)
	}
}

// A submodule inside a repository is its own repository: the innermost .git
// wins, and the outer project keeps the outer one.
func TestInnermostRepoWins(t *testing.T) {
	root := t.TempDir()
	mkdir(t, filepath.Join(root, "outer", ".git"))
	nodeProject(t, filepath.Join(root, "outer"))
	writeFile(t, filepath.Join(root, "outer", "vendor", "sub", ".git"), 20)
	nodeProject(t, filepath.Join(root, "outer", "vendor", "sub", "pkg"))

	if got := repoOf(t, root, "outer/vendor/sub/pkg/node_modules"); got != "outer/vendor/sub" {
		t.Errorf("submodule project: Repo = %q, want outer/vendor/sub", got)
	}
	if got := repoOf(t, root, "outer/node_modules"); got != "outer" {
		t.Errorf("outer project: Repo = %q, want outer", got)
	}
}

// A project in no repository has no Repo.
func TestNoRepoMeansEmpty(t *testing.T) {
	root := t.TempDir()
	nodeProject(t, filepath.Join(root, "loose"))

	if got := repoOf(t, root, "loose/node_modules"); got != "" {
		t.Errorf("Repo = %q, want none", got)
	}
}

// The search never climbs above the scan root. Scanning a folder inside a
// repository reports no repository, because the one that exists is not on
// screen anywhere; scanning the repository itself finds it at the root.
func TestRepoSearchStopsAtTheRoot(t *testing.T) {
	outer := t.TempDir()
	mkdir(t, filepath.Join(outer, ".git"))
	nodeProject(t, filepath.Join(outer, "apps", "web"))

	inside := filepath.Join(outer, "apps")
	if got := repoOf(t, inside, "web/node_modules"); got != "" {
		t.Errorf("scanning inside a repository: Repo = %q, want none", got)
	}
	if got := repoOf(t, outer, "apps/web/node_modules"); got != "." {
		t.Errorf("scanning the repository itself: Repo = %q, want the root", got)
	}
}

// Every item in a monorepo carries an absolute Repo, which is what the table
// keys its heading on.
func TestRepoIsAbsolute(t *testing.T) {
	root := t.TempDir()
	mkdir(t, filepath.Join(root, "yard", ".git"))
	nodeProject(t, filepath.Join(root, "yard", "api"))

	items, _, err := runScan(t, projectOptions(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, it := range items {
		if !filepath.IsAbs(it.Repo) || !strings.EqualFold(it.Repo, filepath.Join(root, "yard")) {
			t.Errorf("%s: Repo = %q, want %q", it.Path, it.Repo, filepath.Join(root, "yard"))
		}
	}
}
