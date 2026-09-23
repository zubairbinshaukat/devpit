//go:build windows

package clean

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// makeTree builds a small nested project tree with a read-only file in it,
// which is the shape Go's RemoveAll used to choke on, and returns its root.
func makeTree(t *testing.T, parent, name string) string {
	t.Helper()
	root := filepath.Join(parent, name)
	dirs := []string{
		filepath.Join(root, "pkg-a", "dist"),
		filepath.Join(root, "pkg-b", ".bin"),
		filepath.Join(root, ".cache", "deep", "deeper"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"index.js", "README.md"} {
			p := filepath.Join(d, f)
			if err := os.WriteFile(p, []byte(strings.Repeat("x", 64)), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	readonly := filepath.Join(root, "pkg-a", "dist", "locked.txt")
	if err := os.WriteFile(readonly, []byte("read only"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(readonly, 0o444); err != nil {
		t.Fatal(err)
	}
	return root
}

// mklinkJunction creates a real NTFS junction, or skips the test when the
// machine will not make one.
func mklinkJunction(t *testing.T, link, target string) {
	t.Helper()
	out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		t.Skipf("mklink /J is unavailable here (%v): %s", err, out)
	}
	if _, statErr := os.Lstat(link); statErr != nil {
		t.Skipf("mklink /J reported success but made nothing: %s", out)
	}
}

// holdFileExclusively opens a file with no sharing at all, which is what a
// real editor or daemon does and what blocks a rename of the folder above it.
// The returned function lets go of the handle; it is safe to call twice and is
// registered as a cleanup either way.
func holdFileExclusively(t *testing.T, path string) (release func()) {
	t.Helper()
	wide, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(wide, windows.GENERIC_READ, 0, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("CreateFile with FILE_SHARE_NONE: %v", err)
	}
	var once sync.Once
	release = func() { once.Do(func() { _ = windows.CloseHandle(h) }) }
	t.Cleanup(release)
	return release
}

// tombstoneDir renames path out of the way the way a real delete does and
// returns the tombstone, insisting that the marker was written: every test
// below that sweeps or retries depends on that marker being there, and a test
// that quietly lost it would be testing the wrong thing.
func tombstoneDir(t *testing.T, path string) string {
	t.Helper()
	tomb, err := tombstone(path)
	if err != nil {
		t.Fatal(err)
	}
	if tomb.MarkerErr != nil {
		t.Fatalf("the tombstone marker was not written: %v", tomb.MarkerErr)
	}
	return tomb.Path
}

// assertMarked checks the marker inside a tombstone names the path it came
// from, which is the claim a sweep checks before it removes anything.
func assertMarked(t *testing.T, tomb, original string) {
	t.Helper()
	m, err := readTombstoneMarker(tomb)
	if err != nil {
		t.Fatalf("reading %s in %s: %v", TombstoneMarkerName, tomb, err)
	}
	if !strings.EqualFold(m.OriginalPath, original) {
		t.Errorf("the marker says %q, want %q", m.OriginalPath, original)
	}
	if m.PID != os.Getpid() {
		t.Errorf("the marker says PID %d, want %d", m.PID, os.Getpid())
	}
	if m.Version == "" {
		t.Error("the marker carries no version")
	}
	if m.CreatedAt.IsZero() {
		t.Error("the marker carries no timestamp")
	}
	if err := verifyTombstone(tomb); err != nil {
		t.Errorf("verifyTombstone(%s) = %v, want nil", tomb, err)
	}
}

// The tombstone rename happens first and the parallel removal finishes the
// job, read-only files and all. Nothing named like the original is left.
func TestTombstoneRenameThenDeleteRemovesAReadOnlyTree(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	target := makeTree(t, base, "node_modules")

	var maxChildTotal int
	rep := Run(context.Background(), []Item{{Path: target, Size: 4096, Tier: TierSafe}},
		Options{Workers: 4}, func(p Progress) {
			if p.ChildTotal > maxChildTotal {
				maxChildTotal = p.ChildTotal
			}
		})

	if len(rep.Skipped) != 0 {
		t.Fatalf("skipped: %+v", rep.Skipped)
	}
	if len(rep.Deleted) != 1 {
		t.Fatalf("deleted %d items, want 1", len(rep.Deleted))
	}
	if rep.Freed != 4096 {
		t.Errorf("Freed = %d, want 4096", rep.Freed)
	}
	if maxChildTotal != 3 {
		t.Errorf("child progress topped out at %d, want the tree's 3 top-level children", maxChildTotal)
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the original is still there: %v", err)
	}
	leftovers, _ := os.ReadDir(base)
	if len(leftovers) != 0 {
		t.Errorf("tombstone left behind: %v", leftovers)
	}
	if !strings.Contains(rep.Deleted[0].Reason, "node_modules") {
		t.Errorf("reason = %q", rep.Deleted[0].Reason)
	}
}

// Safety rule 8, the descendant half: a junction inside the target is removed
// as a link. Whatever it points at survives untouched.
func TestJunctionInsideTheTargetIsRemovedAsALinkAndTheTargetSurvives(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	keep := filepath.Join(base, "real-source")
	if err := os.MkdirAll(keep, 0o755); err != nil {
		t.Fatal(err)
	}
	canary := filepath.Join(keep, "main.go")
	if err := os.WriteFile(canary, []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	junk := makeTree(t, base, "node_modules")
	mklinkJunction(t, filepath.Join(junk, "linked"), keep)

	rep := Run(context.Background(), []Item{{Path: junk, Size: 1, Tier: TierSafe}}, Options{}, nil)
	if len(rep.Skipped) != 0 {
		t.Fatalf("skipped: %+v", rep.Skipped)
	}
	if _, err := os.Lstat(junk); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the junk tree survived: %v", err)
	}
	if _, err := os.Stat(canary); err != nil {
		t.Fatalf("the junction's target was deleted through the link: %v", err)
	}
}

// Safety rule 8, the ancestor half: a path reached through a junction is
// refused outright.
func TestPreflightRefusesAPathReachedThroughAJunction(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	actual := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(actual, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	mklinkJunction(t, link, actual)

	through := filepath.Join(link, "node_modules")
	if err := Preflight(Item{Path: through}, Options{}); !errors.Is(err, ErrReparsePoint) {
		t.Fatalf("Preflight(%q) err = %v, want ErrReparsePoint", through, err)
	}
	// The junction itself is a reparse point too.
	if err := Preflight(Item{Path: link}, Options{}); !errors.Is(err, ErrReparsePoint) {
		t.Fatalf("Preflight(%q) err = %v, want ErrReparsePoint", link, err)
	}
	if _, err := os.Stat(filepath.Join(actual, "node_modules")); err != nil {
		t.Fatal("the pre-flight removed something")
	}
}

// Safety rule 11: drive roots and the Windows directory.
func TestPreflightRefusesDriveRootsAndWindows(t *testing.T) {
	t.Parallel()
	for _, root := range []string{`C:\`, `C:`, `C:\.`, os.Getenv("SystemDrive") + `\`} {
		if err := Preflight(Item{Path: root}, Options{}); !errors.Is(err, ErrDriveRoot) {
			t.Errorf("Preflight(%q) err = %v, want ErrDriveRoot", root, err)
		}
	}
	win := os.Getenv("windir")
	if win == "" {
		t.Skip("windir is not set")
	}
	for _, p := range []string{win, filepath.Join(win, "Temp"), filepath.Join(strings.ToLower(win), "System32", "drivers")} {
		if err := Preflight(Item{Path: p}, Options{}); !errors.Is(err, ErrWindowsDir) {
			t.Errorf("Preflight(%q) err = %v, want ErrWindowsDir", p, err)
		}
	}
}

// A locked folder is discovered by the rename, before a byte is removed, and
// the Restart Manager names the holder — which here is this very test binary.
func TestLockedItemIsReportedWithItsHolder(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	target := makeTree(t, base, "node_modules")
	held := filepath.Join(target, "pkg-a", "dist", "index.js")
	holdFileExclusively(t, held)

	rep := Run(context.Background(), []Item{{Path: target, Size: 9999, Tier: TierSafe}}, Options{}, nil)

	if len(rep.Deleted) != 0 {
		t.Fatalf("deleted a locked item: %+v", rep.Deleted)
	}
	if len(rep.Skipped) != 1 {
		t.Fatalf("skipped %d items, want 1", len(rep.Skipped))
	}
	if rep.Freed != 0 {
		t.Errorf("Freed = %d, want 0: the rename never happened", rep.Freed)
	}
	res := rep.Skipped[0]
	var locked *LockedError
	if !errors.As(res.Err, &locked) {
		t.Fatalf("err = %v (%T), want *LockedError", res.Err, res.Err)
	}
	if locked.Tombstone != "" {
		t.Errorf("Tombstone = %q, want empty: the rename failed", locked.Tombstone)
	}
	self := filepath.Base(os.Args[0])
	found := false
	for _, h := range res.Holders {
		if sameProcessName(h.Name, self) {
			found = true
		}
	}
	if !found && len(res.Holders) == 0 {
		t.Skipf("the Restart Manager named no holder for %s; it is not available in every session", target)
	}
	if !found {
		t.Errorf("Holders = %+v, want one named %q", res.Holders, self)
	}
	if !strings.Contains(res.Reason, "press R to retry") {
		t.Errorf("reason = %q", res.Reason)
	}
	// Nothing was touched: the rename is the lock probe.
	if _, err := os.Stat(held); err != nil {
		t.Errorf("the locked tree was disturbed: %v", err)
	}
}

// Safety rule 7 for a real delete, not just the pre-flight: Devpit never
// removes the folder its own program file lives in.
func TestRunRefusesToDeleteItsOwnDirectory(t *testing.T) {
	t.Parallel()
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable: %v", err)
	}
	dir := filepath.Dir(exe)
	rep := Run(context.Background(), []Item{{Path: dir, Size: 1, Tier: TierSafe}}, Options{}, nil)
	if len(rep.Skipped) != 1 || !errors.Is(rep.Skipped[0].Err, ErrOwnExecutable) {
		t.Fatalf("report = %+v", rep)
	}
	if _, err := os.Stat(exe); err != nil {
		t.Fatalf("the test binary is gone: %v", err)
	}
}

// Step 7: a dry run performs the pre-flight and nothing else.
func TestDryRunDeletesNothing(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	target := makeTree(t, base, "node_modules")
	refused := filepath.Join(base, "protected")
	if err := os.MkdirAll(refused, 0o755); err != nil {
		t.Fatal(err)
	}

	rep := Run(context.Background(), []Item{
		{Path: target, Size: 2048, Tier: TierSafe},
		{Path: refused, Size: 1, Tier: TierReview},
	}, Options{DryRun: true, NeverTouch: []string{refused}}, nil)

	if len(rep.Deleted) != 1 || len(rep.Skipped) != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if rep.Freed != 2048 {
		t.Errorf("Freed = %d, want the 2048 it would have freed", rep.Freed)
	}
	if !strings.HasPrefix(rep.Deleted[0].Reason, "Would delete") {
		t.Errorf("reason = %q", rep.Deleted[0].Reason)
	}
	if _, err := os.Stat(filepath.Join(target, "pkg-a", "dist", "index.js")); err != nil {
		t.Fatalf("a dry run deleted something: %v", err)
	}
	entries, _ := os.ReadDir(base)
	if len(entries) != 2 {
		t.Fatalf("a dry run changed the directory: %v", entries)
	}
}

// Cancelling finishes the item in flight — completed deletions stay done —
// and reports what was done, with every untouched item named and counted.
func TestCancelFinishesTheItemInFlightAndReportsPartially(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	first := makeTree(t, base, "first_modules")
	second := makeTree(t, base, "second_modules")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rep := Run(ctx, []Item{
		{Path: first, Size: 100, Tier: TierSafe},
		{Path: second, Size: 200, Tier: TierSafe},
	}, Options{Workers: 2}, func(p Progress) {
		if p.Index == 0 && p.ChildTotal > 0 {
			cancel()
		}
	})

	if len(rep.Deleted) != 1 {
		t.Fatalf("deleted = %+v, want the item in flight to finish", rep.Deleted)
	}
	if len(rep.Skipped) != 1 {
		t.Fatalf("skipped = %+v, want the untouched item named", rep.Skipped)
	}
	if !errors.Is(rep.Skipped[0].Err, context.Canceled) {
		t.Errorf("skipped err = %v, want context.Canceled", rep.Skipped[0].Err)
	}
	if rep.Freed != 100 {
		t.Errorf("Freed = %d, want 100", rep.Freed)
	}
	if _, err := os.Lstat(first); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the item in flight did not finish: %v", err)
	}
	if _, err := os.Stat(filepath.Join(second, "pkg-a", "dist", "index.js")); err != nil {
		t.Errorf("the cancelled item was touched: %v", err)
	}
	// A finished item never leaves a tombstone behind.
	for _, e := range mustReadDir(t, base) {
		if IsTombstone(e) {
			t.Errorf("tombstone left behind after a clean finish: %s", e)
		}
	}
}

// An interrupted removal leaves a tombstone, never a half-emptied folder that
// looks like a working one, and Sweep finishes it later.
func TestSweepFinishesATombstoneLeftByALockedRemoval(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	target := makeTree(t, base, "node_modules")

	// Rename succeeds, then the removal hits a handle nothing will give up.
	tomb := tombstoneDir(t, target)
	assertMarked(t, tomb, target)
	held := filepath.Join(tomb, "pkg-a", "dist", "index.js")
	release := holdFileExclusively(t, held)
	if err := removeTree(tomb, 4, nil); err == nil {
		t.Fatal("expected the held file to block the removal")
	}
	if _, err := os.Lstat(tomb); err != nil {
		t.Fatalf("the tombstone did not survive the failure: %v", err)
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the original name came back")
	}

	found := FindTombstones(context.Background(), []string{base})
	if len(found) != 1 || !strings.EqualFold(found[0], tomb) {
		t.Fatalf("FindTombstones = %v, want [%s]", found, tomb)
	}

	// Once the holder lets go, the sweep finishes the job.
	release()
	rep := Resume(context.Background(), []string{base}, Options{Workers: 4}, nil)
	if len(rep.Skipped) != 0 {
		t.Fatalf("sweep skipped: %+v", rep.Skipped)
	}
	if len(rep.Deleted) != 1 {
		t.Fatalf("sweep deleted %d, want 1", len(rep.Deleted))
	}
	if _, err := os.Lstat(tomb); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the tombstone survived the sweep: %v", err)
	}
}

// Retry finishes a tombstone an earlier attempt left behind rather than
// starting the item over.
func TestRetryFinishesAnExistingTombstone(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	target := makeTree(t, base, "node_modules")
	tomb := tombstoneDir(t, target)

	res := Retry(context.Background(), Item{Path: target, Size: 64, Tier: TierSafe}, Options{}, nil)
	if res.Err != nil {
		t.Fatalf("Retry: %v", res.Err)
	}
	if _, err := os.Lstat(tomb); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the tombstone survived the retry: %v", err)
	}
}

// RetryLocked is consulted once and answering no stops there.
func TestRetryLockedIsAskedAndHonoured(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	target := makeTree(t, base, "node_modules")
	holdFileExclusively(t, filepath.Join(target, "pkg-b", ".bin", "index.js"))

	asked := 0
	rep := Run(context.Background(), []Item{{Path: target, Size: 1, Tier: TierSafe}}, Options{
		RetryLocked: func(le LockedError) bool {
			asked++
			if le.Path != target {
				t.Errorf("LockedError.Path = %q, want %q", le.Path, target)
			}
			return asked < 3
		},
	}, nil)

	if asked != 3 {
		t.Errorf("RetryLocked was asked %d times, want 3", asked)
	}
	if len(rep.Skipped) != 1 {
		t.Fatalf("report = %+v", rep)
	}
}

// Review and Careful items skip the tombstone and go to the Recycle Bin whole,
// so they are reversible.
func TestReviewItemsGoToTheRecycleBin(t *testing.T) {
	t.Parallel()
	requireRecycleBin(t)
	base := t.TempDir()
	target := makeTree(t, base, "dist")

	rep := Run(context.Background(), []Item{{Path: target, Size: 512, Tier: TierReview}}, Options{}, nil)
	if len(rep.Skipped) != 0 {
		t.Fatalf("skipped: %+v", rep.Skipped)
	}
	if rep.Freed != 512 {
		t.Errorf("Freed = %d, want 512", rep.Freed)
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the item is still there: %v", err)
	}
	// No tombstone was made on the way.
	for _, e := range mustReadDir(t, base) {
		if IsTombstone(e) {
			t.Errorf("a Recycle Bin move made a tombstone: %s", e)
		}
	}
	if !strings.Contains(rep.Deleted[0].Reason, "Recycle Bin") {
		t.Errorf("reason = %q", rep.Deleted[0].Reason)
	}
}

// A path over MAX_PATH is removed, whatever LongPathsEnabled says.
func TestRemovesAPathOverMaxPath(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	target := filepath.Join(base, "node_modules")
	deep := target
	for i := 0; i < 8 && len(deep) < 320; i++ {
		deep = filepath.Join(deep, strings.Repeat("d", 40))
	}
	if len(deep) <= 260 {
		t.Skipf("could not build a path over MAX_PATH from %q", base)
	}
	if err := os.MkdirAll(longPath(deep), 0o755); err != nil {
		t.Skipf("MkdirAll on a long path: %v", err)
	}
	if err := os.WriteFile(longPath(filepath.Join(deep, "f.txt")), []byte("x"), 0o644); err != nil {
		t.Skipf("WriteFile on a long path: %v", err)
	}

	rep := Run(context.Background(), []Item{{Path: target, Size: 1, Tier: TierSafe}}, Options{}, nil)
	if len(rep.Skipped) != 0 {
		t.Fatalf("skipped: %+v", rep.Skipped)
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the long tree survived: %v", err)
	}
}

// Regression test for the pointer-lifetime bug moveToTrash works around
// (see the comment on moveToTrash in recyclebin_windows.go): go2trash's
// Windows implementation stashed a UTF-16 buffer's address as a bare uintptr
// in a struct field and never kept the buffer alive past that point, so the
// backing array could be collected while SHFileOperationW was still reading
// it. That window is normally microseconds, which is why the bug only ever
// showed up once, as a fatal crash, during a full `go test ./...` run with
// every package allocating at once. Hammering moveToTrash from many
// goroutines while another goroutine forces GC continuously is the closest
// thing to a reliable repro this package can offer for a race that otherwise
// depends on scheduler and GC timing outside the test's control.
func TestMoveToTrashSurvivesConcurrentGC(t *testing.T) {
	t.Parallel()
	base := t.TempDir()

	stop := make(chan struct{})
	var gcWG sync.WaitGroup
	gcWG.Add(1)
	go func() {
		defer gcWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
				runtime.GC()
			}
		}
	}()

	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 15; i++ {
				p := filepath.Join(base, fmt.Sprintf("f-%d-%d.txt", worker, i))
				if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
					t.Error(err)
					return
				}
				if err := moveToTrash(p); err != nil {
					t.Errorf("moveToTrash(%q): %v", p, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(stop)
	gcWG.Wait()
}

func mustReadDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// countFiles returns every file under root with its contents, so a test can
// prove that nothing was touched rather than merely that the root still
// exists.
func countFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	out := make(map[string]string)
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, readErr := os.ReadFile(p) //nolint:gosec // a test fixture under t.TempDir
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		out[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Safety rules 1 and 6, at the sweep. A folder whose name happens to match
// Devpit's tombstone pattern is a folder the user made, and a sweep must leave
// every byte of it alone. The name proves nothing; the marker does.
func TestSweepIgnoresALookAlikeWithoutAMarker(t *testing.T) {
	t.Parallel()
	base := t.TempDir()

	// One look-alike with nothing inside to vouch for it, and one that
	// carries a marker pointing at some other path entirely.
	bare := makeTree(t, base, "archive.devpit-abc12345")
	lying := makeTree(t, base, "backup.devpit-zz99zz99")
	marker := TombstoneMarker{
		OriginalPath: filepath.Join(base, "something-else"),
		CreatedAt:    time.Now().UTC(),
		PID:          os.Getpid(),
		Version:      "dev",
	}
	data, err := json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lying, TombstoneMarkerName), data, 0o600); err != nil {
		t.Fatal(err)
	}

	before := map[string]map[string]string{
		bare:  countFiles(t, bare),
		lying: countFiles(t, lying),
	}

	if found := FindTombstones(context.Background(), []string{base}); len(found) != 0 {
		t.Fatalf("FindTombstones = %v, want none: neither folder is Devpit's", found)
	}

	rep := Sweep(context.Background(), []string{base}, Options{Workers: 4}, nil)
	if len(rep.Deleted) != 0 {
		t.Fatalf("the sweep deleted %+v", rep.Deleted)
	}
	if len(rep.Skipped) != 2 {
		t.Fatalf("the sweep skipped %d look-alikes, want 2: %+v", len(rep.Skipped), rep.Skipped)
	}
	for _, r := range rep.Skipped {
		if !errors.Is(r.Err, ErrNotATombstone) {
			t.Errorf("%s was skipped with %v, want ErrNotATombstone", r.Path, r.Err)
		}
		if r.Reason == "" {
			t.Errorf("%s was skipped silently", r.Path)
		}
	}
	for dir, want := range before {
		got := countFiles(t, dir)
		if len(got) != len(want) {
			t.Fatalf("%s has %d files, want %d", dir, len(got), len(want))
		}
		for name, content := range want {
			if got[name] != content {
				t.Errorf("%s: %s changed", dir, name)
			}
		}
	}
}

// The marker goes in the instant the rename succeeds, says where the folder
// came from, and is what a sweep looks for.
func TestTombstoneMarkerIsWrittenOnRename(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	target := makeTree(t, base, "node_modules")

	tomb := tombstoneDir(t, target)
	assertMarked(t, tomb, target)

	found := FindTombstones(context.Background(), []string{base})
	if len(found) != 1 || !strings.EqualFold(found[0], tomb) {
		t.Fatalf("FindTombstones = %v, want [%s]", found, tomb)
	}

	// The marker is the last thing to go, so a removal that fails half way
	// still leaves something a later sweep can act on.
	if err := os.Remove(filepath.Join(tomb, TombstoneMarkerName)); err != nil {
		t.Fatal(err)
	}
	if err := verifyTombstone(tomb); !errors.Is(err, ErrNotATombstone) {
		t.Fatalf("without its marker, verifyTombstone = %v, want ErrNotATombstone", err)
	}
	if found := FindTombstones(context.Background(), []string{base}); len(found) != 0 {
		t.Fatalf("FindTombstones = %v, want none once the marker is gone", found)
	}
}

// Safety rule 6 at the sweep: the never-touch list can grow between the rename
// and the sweep that finishes it, and when it does the leftovers stay.
func TestSweepRefusesATombstoneOnTheNeverTouchList(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	target := makeTree(t, base, "node_modules")
	tomb := tombstoneDir(t, target)

	rep := Sweep(context.Background(), []string{base}, Options{
		Workers:    4,
		NeverTouch: []string{filepath.Join(base, "node_modules")},
	}, nil)

	if len(rep.Deleted) != 0 {
		t.Fatalf("the sweep deleted %+v", rep.Deleted)
	}
	if len(rep.Skipped) != 1 || !errors.Is(rep.Skipped[0].Err, ErrNeverTouch) {
		t.Fatalf("skipped = %+v, want one ErrNeverTouch", rep.Skipped)
	}
	if _, err := os.Lstat(tomb); err != nil {
		t.Fatalf("the tombstone did not survive: %v", err)
	}
	if len(countFiles(t, tomb)) == 0 {
		t.Error("the tombstone was emptied")
	}
}

// Safety rule 8 at the sweep: a junction wearing a tombstone's name is never
// followed, whatever the marker on the other side of it says.
func TestSweepRefusesATombstoneThatIsAReparsePoint(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	target := makeTree(t, t.TempDir(), "real")
	link := filepath.Join(base, "node_modules.devpit-aaaa1111")

	// The marker on the far side of the junction claims exactly what a
	// genuine tombstone's would, so nothing but the reparse check stands
	// between the sweep and someone else's tree.
	data, err := json.Marshal(TombstoneMarker{
		OriginalPath: filepath.Join(base, "node_modules"),
		CreatedAt:    time.Now().UTC(),
		PID:          os.Getpid(),
		Version:      "dev",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, TombstoneMarkerName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	mklinkJunction(t, link, target)
	before := countFiles(t, target)

	if err := preflightTombstone(link, "", Options{}); !errors.Is(err, ErrReparsePoint) {
		t.Errorf("preflightTombstone(%s) = %v, want ErrReparsePoint", link, err)
	}

	rep := Sweep(context.Background(), []string{base}, Options{Workers: 4}, nil)
	if len(rep.Deleted) != 0 {
		t.Fatalf("the sweep deleted %+v", rep.Deleted)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Errorf("the junction was removed: %v", err)
	}
	got := countFiles(t, target)
	if len(got) != len(before) {
		t.Fatalf("the junction's target has %d files, want %d", len(got), len(before))
	}
}

// Safety rule 6 at the retry: pressing R resumes a deletion, and resuming a
// deletion runs the pre-flight again.
func TestRetryRefusesWhenNeverTouchNowCoversTheItem(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	target := makeTree(t, base, "node_modules")
	tomb := tombstoneDir(t, target)

	res := Retry(context.Background(), Item{Path: target, Size: 64, Tier: TierSafe}, Options{
		NeverTouch: []string{base},
	}, nil)

	if !errors.Is(res.Err, ErrNeverTouch) {
		t.Fatalf("Retry err = %v, want ErrNeverTouch", res.Err)
	}
	if res.Reason == "" {
		t.Error("the retry refused silently")
	}
	if len(countFiles(t, tomb)) == 0 {
		t.Error("the tombstone was emptied anyway")
	}
}

// sameProcessName compares two process names ignoring case and a trailing
// ".exe", which the Restart Manager includes on some machines and not on
// others. See the identical helper in internal/winapi.
func sameProcessName(a, b string) bool {
	trim := func(s string) string { return strings.TrimSuffix(strings.ToLower(s), ".exe") }
	return trim(a) == trim(b)
}

// requireRecycleBin skips the calling test when this session cannot reach the
// Recycle Bin. SHFileOperationW is a shell API, and a non-interactive session
// such as a CI runner's answers it with a DE_* code (0x78, "security settings
// denied access to the source") rather than performing the move. That is a
// property of the session, not of the code under test, so the tests that need
// a real Recycle Bin say so and step aside when there is not one.
func requireRecycleBin(t *testing.T) {
	t.Helper()
	probe := filepath.Join(t.TempDir(), "recycle-bin-probe")
	if err := os.WriteFile(probe, []byte("probe"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := moveToTrash(probe); err != nil {
		t.Skipf("the Recycle Bin is not reachable from this session: %v", err)
	}
}
