package scan_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/scan"
)

// An empty match frees nothing, so it is not reported. A results table full
// of zero-byte rows is a results table nobody reads to the bottom of.
func TestEmptyMatchesAreNotReported(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "proj", "package.json"), 40)
	mkdir(t, filepath.Join(root, "proj", "node_modules"))
	writeFile(t, filepath.Join(root, "other", "package.json"), 40)
	writeFile(t, filepath.Join(root, "other", "node_modules", "dep", "i.js"), 10)

	items, _, err := runScan(t, projectOptions(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, rel := range relPaths(t, root, items) {
		if strings.HasPrefix(rel, "proj/") {
			t.Errorf("listed %s, which is empty", rel)
		}
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want only the one with bytes in it: %v", len(items), relPaths(t, root, items))
	}
}

// Roots that contain one another are walked without listing anything twice.
// A user who adds both D:\work and D:\work\api to their folder list must not
// be shown the same gigabytes in two rows.
func TestOverlappingRootsListEachPathOnce(t *testing.T) {
	root := buildTree(t)
	opts := projectOptions(root)
	opts.Roots = []string{root, filepath.Join(root, "web"), filepath.Join(root, "api")}

	items, stats, err := runScan(t, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	seen := map[string]int{}
	for _, it := range items {
		seen[strings.ToLower(it.Path)]++
	}
	for path, n := range seen {
		if n > 1 {
			t.Errorf("%s was listed %d times", path, n)
		}
	}
	if stats.Found != uint64(len(items)) {
		t.Errorf("Stats.Found = %d but %d items arrived", stats.Found, len(items))
	}
}

// A rule that matches nothing usable is dropped rather than failing the scan.
// A broken rule should not stop a user reclaiming space with the other forty.
func TestInvalidRulesAreIgnoredNotFatal(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "proj", "package.json"), 40)
	writeFile(t, filepath.Join(root, "proj", "node_modules", "dep", "i.js"), 100)

	opts := scan.Options{
		Roots:   []string{root},
		Workers: 2,
		Rules: []scan.Rule{
			{Name: "broken: no restore hint", Names: []string{"node_modules"}},
			{
				Name: "node_modules", Names: []string{"node_modules"},
				Markers: []string{"package.json"}, Tier: scan.TierSafe,
				RestoreHint: "Run npm install.",
			},
		},
	}
	items, _, err := runScan(t, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].RestoreHint == "" {
		t.Error("the broken rule was used instead of the complete one")
	}
}

// A never-touch entry that names a folder deeper than the root still applies,
// and one that names nothing existing is harmless.
func TestNeverTouchEntriesAreMatchedByPrefix(t *testing.T) {
	root := buildTree(t)
	opts := projectOptions(root)
	opts.NeverTouch = []string{
		filepath.Join(root, "web", "node_modules"),
		filepath.Join(root, "does-not-exist"),
		"   ",
	}

	items, _, err := runScan(t, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := relPaths(t, root, items)
	if contains(got, "web/node_modules") {
		t.Error("a never-touch entry naming the folder itself did not exclude it")
	}
	if !contains(got, "web/dist") {
		t.Error("a never-touch entry excluded a sibling it does not name")
	}
}
