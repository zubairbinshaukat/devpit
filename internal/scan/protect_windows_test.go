package scan_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/protect"
)

// Rules 8 and 30 together: a junction inside a project that points at a
// login folder is still a leaf. The project's node_modules is reported as
// before, sized without anything behind the link, and nothing from the
// login folder is reported through it. Protecting login folders changes
// nothing about how links are treated.
func TestAJunctionToALoginFolderIsStillALeaf(t *testing.T) {
	t.Parallel()
	home := fakeProfile(t)
	writeFile(t, filepath.Join(home, ".claude", "projects", "big.jsonl"), 5000)

	proj := filepath.Join(t.TempDir(), "proj")
	writeFile(t, filepath.Join(proj, "package.json"), 40)
	writeFile(t, filepath.Join(proj, "node_modules", "dep", "index.js"), 100)
	if !makeJunction(t, filepath.Join(proj, "node_modules", "claude"), filepath.Join(home, ".claude")) {
		t.Skip("this account cannot create a junction")
	}
	// A junction at the top of the scan root that leads into the login
	// folder, where a node_modules sits.
	hasTopLink := makeJunction(t, filepath.Join(proj, "linked-claude"), filepath.Join(home, ".claude", "local"))

	opts := projectOptions(proj)
	opts.Protect = protect.Default(homeEnv(home))
	items, _, err := runScan(t, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	got, ok := findItem(items, "proj/node_modules")
	if !ok {
		t.Fatalf("proj/node_modules was not listed; got %v", relPaths(t, proj, items))
	}
	if got.Size != 100 {
		t.Errorf("size = %d, want 100: the sizer counted bytes through the junction into .claude", got.Size)
	}
	for _, rel := range relPaths(t, proj, items) {
		lower := strings.ToLower(rel)
		if strings.Contains(lower, "claude") {
			t.Errorf("listed %s, which is only reachable through a junction into .claude", rel)
		}
		if hasTopLink && strings.HasPrefix(lower, "linked-claude") {
			t.Errorf("listed %s through a top-level junction", rel)
		}
	}
}
