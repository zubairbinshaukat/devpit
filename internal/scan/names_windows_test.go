package scan_test

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A directory whose name ends in a dot or a space can only be created, and
// only be opened, through the \?\ prefix. Every ordinary Win32 path call
// strips those characters and lands on a path that does not exist.
//
// The walker has to survive one. fastwalk reports a directory it could not
// read by calling the callback a second time with the error, and whatever
// that call returns becomes the error for the whole walk: returning SkipDir
// there, which is the obvious thing to write, aborts the scan of an entire
// drive over one oddly named folder.
func TestUnreadableDirectoryDoesNotAbortTheScan(t *testing.T) {
	root := t.TempDir()

	// Real findings on both sides of the awkward name, so an abort is
	// obvious in the results rather than only in the error.
	writeFile(t, filepath.Join(root, "a-project", "package.json"), 40)
	writeFile(t, filepath.Join(root, "a-project", "node_modules", "dep", "i.js"), 100)
	writeFile(t, filepath.Join(root, "z-project", "package.json"), 40)
	writeFile(t, filepath.Join(root, "z-project", "node_modules", "dep", "i.js"), 200)

	// Go's own os.Mkdir cleans the path before it reaches the system call,
	// which loses the prefix and with it the whole point. The raw call is the
	// only way to make the name.
	odd := root + string(filepath.Separator) + "trailing-dot."
	raw, err := syscall.UTF16PtrFromString(`\\?\` + odd)
	if err != nil {
		t.Fatalf("encoding the path: %v", err)
	}
	if err := syscall.CreateDirectory(raw, nil); err != nil {
		t.Skipf("this filesystem will not create a name ending in a dot: %v", err)
	}

	items, stats, rerr := runScan(t, projectOptions(root))
	if rerr != nil {
		t.Fatalf("Run failed because of a folder it could not read: %v", rerr)
	}

	got := relPaths(t, root, items)
	for _, want := range []string{"a-project/node_modules", "z-project/node_modules"} {
		if !contains(got, want) {
			t.Errorf("did not find %s; the walk stopped early. Got %v", want, got)
		}
	}
	if stats.Found != 2 {
		t.Errorf("Stats.Found = %d, want 2", stats.Found)
	}
	if !strings.HasSuffix(odd, ".") {
		t.Fatal("the fixture for this test is wrong")
	}
}
