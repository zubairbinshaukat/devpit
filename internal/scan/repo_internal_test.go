package scan

import (
	"os"
	"path/filepath"
	"testing"
)

// The climb is memoised per directory: once a project's lookup has passed
// through a folder, a sibling under it is answered from the map without a
// single stat. Removing the .git between the two lookups proves it, because
// an uncached second lookup would find nothing.
func TestRepoLookupIsCachedPerDirectory(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "yard")
	for _, d := range []string{filepath.Join(repo, ".git"), filepath.Join(repo, "apps", "web"), filepath.Join(repo, "apps", "admin")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	c := newRepoCache()
	if got := c.lookup(root, filepath.Join(repo, "apps", "web")); got != repo {
		t.Fatalf("first lookup = %q, want %q", got, repo)
	}
	if err := os.Remove(filepath.Join(repo, ".git")); err != nil {
		t.Fatal(err)
	}
	if got := c.lookup(root, filepath.Join(repo, "apps", "admin")); got != repo {
		t.Errorf("sibling lookup = %q, want the cached %q", got, repo)
	}
}

// A directory outside the root has no repository, whatever is above it: a
// location rule's fixed path was never reached by walking a root.
func TestRepoLookupOutsideTheRootIsEmpty(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	if err := os.MkdirAll(filepath.Join(other, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := newRepoCache()
	if got := c.lookup(root, other); got != "" {
		t.Errorf("lookup outside the root = %q, want none", got)
	}
	if got := c.lookup("", other); got != "" {
		t.Errorf("lookup with no root = %q, want none", got)
	}
}
