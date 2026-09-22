package scan_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/scan"
)

// nodeItem is the item a scan would produce for dir/node_modules.
func nodeItem(dir string) scan.Item {
	return scan.Item{
		Path:        filepath.Join(dir, "node_modules"),
		Name:        "node_modules",
		Project:     dir,
		Rule:        "node_modules",
		Markers:     []string{"package.json"},
		RestoreHint: "Run npm install.",
		Kind:        scan.KindProjectJunk,
	}
}

// Safety rule 10: the marker is re-verified immediately before deleting.
func TestVerifyAcceptsAnUnchangedItem(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), 40)
	writeFile(t, filepath.Join(dir, "node_modules", "dep", "index.js"), 10)

	if err := scan.Verify(nodeItem(dir)); err != nil {
		t.Errorf("Verify rejected an unchanged item: %v", err)
	}
}

// The window between scanning and confirming is where the marker can vanish:
// somebody deletes package.json, and what looked like installed dependencies
// is now a folder nobody can vouch for.
func TestVerifyRefusesWhenTheMarkerIsGone(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "package.json")
	writeFile(t, marker, 40)
	writeFile(t, filepath.Join(dir, "node_modules", "dep", "index.js"), 10)

	if err := os.Remove(marker); err != nil {
		t.Fatalf("removing the marker: %v", err)
	}
	err := scan.Verify(nodeItem(dir))
	if !errors.Is(err, scan.ErrMarkerMissing) {
		t.Errorf("error = %v, want ErrMarkerMissing", err)
	}
}

// An item the scan already flagged as unverified is not asked for a marker it
// never had; its own flag is the record of that.
func TestVerifyDoesNotDemandAMarkerFromAnUnverifiedItem(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "node_modules", "dep", "index.js"), 10)

	it := nodeItem(dir)
	it.Unverified = true
	if err := scan.Verify(it); err != nil {
		t.Errorf("Verify rejected an unverified item: %v", err)
	}
}

// A folder deleted in Explorer between the scan and the confirmation is
// reported as gone rather than as an error nobody can act on.
func TestVerifyRefusesAMissingPath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), 40)

	err := scan.Verify(nodeItem(dir))
	if !errors.Is(err, scan.ErrGone) {
		t.Errorf("error = %v, want ErrGone", err)
	}
}

// A folder that was renamed is not the folder the user ticked.
func TestVerifyRefusesARenamedPath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), 40)
	writeFile(t, filepath.Join(dir, "node_modules", "dep", "index.js"), 10)

	it := nodeItem(dir)
	it.Name = "something_else"
	err := scan.Verify(it)
	if !errors.Is(err, scan.ErrRenamed) {
		t.Errorf("error = %v, want ErrRenamed", err)
	}
}

// A directory that has become a file, or the other way round, is refused.
func TestVerifyRefusesAChangedType(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), 40)
	writeFile(t, filepath.Join(dir, "node_modules"), 10) // now a file

	err := scan.Verify(nodeItem(dir))
	if !errors.Is(err, scan.ErrNotADirectory) {
		t.Errorf("error = %v, want ErrNotADirectory", err)
	}
}

// Safety rule 11's scan-side half: a drive root, a relative path and a UNC
// path are refused outright, whatever else is true about them.
func TestVerifyRefusesProtectedPaths(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{"empty", ""},
		{"relative", filepath.Join("some", "where")},
		{"unc", `\\server\share\node_modules`},
		{"drive root", filepath.VolumeName(mustAbs(t)) + string(filepath.Separator)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			it := scan.Item{Path: tc.path, Name: filepath.Base(tc.path)}
			if err := scan.Verify(it); !errors.Is(err, scan.ErrProtectedPath) {
				t.Errorf("error = %v, want ErrProtectedPath", err)
			}
		})
	}
}

// A rule with no markers has nothing to re-verify, and Verify says so rather
// than inventing a requirement.
func TestVerifyAcceptsAMarkerlessRule(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "__pycache__")
	mkdir(t, cache)

	it := scan.Item{Path: cache, Name: "__pycache__", Rule: "pycache", RestoreHint: "Python rewrites it."}
	if err := scan.Verify(it); err != nil {
		t.Errorf("Verify rejected a markerless item: %v", err)
	}
}

// A rule whose markers live inside the folder rather than beside it, which is
// how Unity projects are recognised, is checked in the right place.
func TestVerifyChecksMarkersInsideWhenTheRuleSaysSo(t *testing.T) {
	dir := t.TempDir()
	inside := filepath.Join(dir, "Library")
	writeFile(t, filepath.Join(inside, "marker.txt"), 10)

	it := scan.Item{
		Path: inside, Name: "Library", Rule: "unity library",
		Markers: []string{"marker.txt"}, MarkersInside: true,
		RestoreHint: "Unity reimports.",
	}
	if err := scan.Verify(it); err != nil {
		t.Errorf("Verify rejected an item whose marker is inside it: %v", err)
	}

	it.MarkersInside = false
	if err := scan.Verify(it); !errors.Is(err, scan.ErrMarkerMissing) {
		t.Errorf("error = %v, want ErrMarkerMissing when the marker is looked for beside instead", err)
	}
}

// A large file item is a file, so Verify expects one.
func TestVerifyAcceptsALargeFile(t *testing.T) {
	dir := t.TempDir()
	iso := filepath.Join(dir, "windows.iso")
	writeFile(t, iso, 100)

	it := scan.Item{Path: iso, Name: "windows.iso", Kind: scan.KindLargeFile, RestoreHint: "Download it again."}
	if err := scan.Verify(it); err != nil {
		t.Errorf("Verify rejected a large file: %v", err)
	}
}

// IsReparsePoint on an ordinary folder is false, and on a missing path it is
// an error rather than a quiet false.
func TestIsReparsePointOnOrdinaryPaths(t *testing.T) {
	dir := t.TempDir()
	is, err := scan.IsReparsePoint(dir)
	if err != nil {
		t.Fatalf("IsReparsePoint: %v", err)
	}
	if is {
		t.Error("a temporary directory was reported as a reparse point")
	}

	if _, err := scan.IsReparsePoint(filepath.Join(dir, "missing")); err == nil {
		t.Error("IsReparsePoint on a missing path returned no error")
	}
}

// mustAbs returns an absolute path on the current volume, for the drive-root
// case above.
func mustAbs(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(t.TempDir())
	if err != nil {
		t.Fatalf("resolving a temporary directory: %v", err)
	}
	return abs
}
