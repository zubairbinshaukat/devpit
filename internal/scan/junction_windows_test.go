package scan_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/scan"
)

// Safety rule 8, with a real junction rather than a mock.
//
// This is the bug that made npkill delete people's source code: a junction
// inside node_modules points back at a real folder, the walker follows it,
// and the folder is reported as junk. Devpit treats every reparse point as a
// leaf, so the junction is neither descended into during the walk nor
// counted during sizing.
func TestJunctionIsNeverFollowedOrSizedThrough(t *testing.T) {
	root := t.TempDir()

	// The real source that must survive, and be large enough that sizing
	// through the junction would be obvious in the numbers.
	writeFile(t, filepath.Join(root, "source", "package.json"), 40)
	writeFile(t, filepath.Join(root, "source", "big.js"), 5000)

	// A project whose node_modules holds a junction to that source.
	writeFile(t, filepath.Join(root, "proj", "package.json"), 40)
	writeFile(t, filepath.Join(root, "proj", "node_modules", "dep", "index.js"), 100)
	if !makeJunction(t, filepath.Join(root, "proj", "node_modules", "linked"), filepath.Join(root, "source")) {
		t.Skip("this account cannot create a junction")
	}

	// A junction at the top of the tree, pointing at the project.
	hasTopLink := makeJunction(t, filepath.Join(root, "mirror"), filepath.Join(root, "proj"))

	items, _, err := runScan(t, projectOptions(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	got, ok := findItem(items, "proj/node_modules")
	if !ok {
		t.Fatalf("proj/node_modules was not listed; got %v", relPaths(t, root, items))
	}
	if got.Size != 100 {
		t.Errorf("size = %d, want 100: the sizer counted bytes through the junction", got.Size)
	}

	for _, rel := range relPaths(t, root, items) {
		lower := strings.ToLower(rel)
		if strings.Contains(lower, "linked") {
			t.Errorf("listed %s, which is only reachable through a junction", rel)
		}
		if hasTopLink && strings.HasPrefix(lower, "mirror") {
			t.Errorf("listed %s, which is only reachable through a top-level junction", rel)
		}
	}
}

// IsReparsePoint and ResolvesThroughReparse answer the two questions the
// delete pre-flight asks: is this thing a link, and is anything above it one.
func TestReparsePointDetection(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "target", "file.txt"), 10)
	link := filepath.Join(root, "link")
	if !makeJunction(t, link, filepath.Join(root, "target")) {
		t.Skip("this account cannot create a junction")
	}

	is, err := scan.IsReparsePoint(link)
	if err != nil {
		t.Fatalf("IsReparsePoint(%s): %v", link, err)
	}
	if !is {
		t.Error("a junction was not recognised as a reparse point")
	}

	is, err = scan.IsReparsePoint(filepath.Join(root, "target"))
	if err != nil {
		t.Fatalf("IsReparsePoint(target): %v", err)
	}
	if is {
		t.Error("an ordinary directory was reported as a reparse point")
	}

	through, err := scan.ResolvesThroughReparse(filepath.Join(link, "file.txt"))
	if err != nil {
		t.Fatalf("ResolvesThroughReparse: %v", err)
	}
	if !through {
		t.Error("a path below a junction was not reported as resolving through one")
	}

	through, err = scan.ResolvesThroughReparse(filepath.Join(root, "target", "file.txt"))
	if err != nil {
		t.Fatalf("ResolvesThroughReparse: %v", err)
	}
	if through {
		t.Error("an ordinary path was reported as resolving through a reparse point")
	}
}

// Verify refuses an item whose path now resolves through a junction, which is
// what stops the delete engine renaming inside somebody's real source folder.
func TestVerifyRefusesAPathBelowAJunction(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "real", "package.json"), 40)
	writeFile(t, filepath.Join(root, "real", "node_modules", "dep", "x.js"), 10)
	link := filepath.Join(root, "link")
	if !makeJunction(t, link, filepath.Join(root, "real")) {
		t.Skip("this account cannot create a junction")
	}

	it := scan.Item{
		Path:    filepath.Join(link, "node_modules"),
		Name:    "node_modules",
		Markers: []string{"package.json"},
		Rule:    "node_modules",
	}
	if err := scan.Verify(it); err == nil {
		t.Fatal("Verify accepted a path that resolves through a junction")
	}
}
