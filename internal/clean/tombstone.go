package clean

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// TombstoneSuffix is the marker Devpit puts between an item's name and the
// random tail when it renames it out of the way.
const TombstoneSuffix = ".devpit-"

// tombstoneTailLen is how many random characters follow the suffix. Eight from
// a 36-character alphabet is far more than enough to avoid a collision inside
// one directory, and short enough to read in Explorer.
const tombstoneTailLen = 8

// tombstoneAlphabet is deliberately lowercase and digits only: no characters
// that a shell, a glob or a case-insensitive filesystem would treat specially.
const tombstoneAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// randomTail returns tombstoneTailLen characters from tombstoneAlphabet. It
// uses crypto/rand because it is the only source that cannot be seeded into
// producing the same name twice in a fast loop.
func randomTail() (string, error) {
	buf := make([]byte, tombstoneTailLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("clean: generating a tombstone name: %w", err)
	}
	for i, b := range buf {
		buf[i] = tombstoneAlphabet[int(b)%len(tombstoneAlphabet)]
	}
	return string(buf), nil
}

// IsTombstone reports whether a name is one of Devpit's tombstones.
func IsTombstone(name string) bool {
	base := filepath.Base(name)
	i := strings.LastIndex(base, TombstoneSuffix)
	if i < 0 {
		return false
	}
	tail := base[i+len(TombstoneSuffix):]
	if len(tail) != tombstoneTailLen {
		return false
	}
	for _, r := range tail {
		if !strings.ContainsRune(tombstoneAlphabet, r) {
			return false
		}
	}
	return true
}

// tombstone renames path to "<name><TombstoneSuffix><8 random>" in the same
// directory and returns the new path. The rename is atomic and instant, which
// is why it happens before a single byte is removed: the space can be reported
// freed immediately, a crash can never leave a half-emptied folder that looks
// like a working one, and a sharing violation here is the cheapest way to find
// out the folder is locked.
//
// The moment the rename succeeds, a marker file goes inside the new directory
// ([TombstoneMarkerName]). That marker is the only thing that ever makes a
// later sweep willing to remove the directory: the name on its own is
// something any user could have typed.
func tombstone(path string) (tombstoneResult, error) {
	tomb, err := renameToTombstone(path)
	if err != nil {
		return tombstoneResult{}, err
	}
	return tombstoneResult{Path: tomb, MarkerErr: writeTombstoneMarker(tomb)}, nil
}

// tombstoneResult is a successful rename: where the directory went, and
// whether it could be marked as Devpit's.
type tombstoneResult struct {
	// Path is the renamed directory.
	Path string
	// MarkerErr is non-nil when [TombstoneMarkerName] could not be written.
	// The deletion still goes ahead — its pre-flight passed before the rename
	// and nothing about a failed marker makes removing the tree less safe —
	// but an unmarked leftover is one no sweep will ever finish, because a
	// sweep cannot tell it apart from a folder the user named that way. The
	// caller says so in the reason it reports.
	MarkerErr error
}

// renameToTombstone is the rename half of [tombstone], separated so the marker
// write has exactly one call site.
func renameToTombstone(path string) (string, error) {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	var lastErr error
	// A name collision is vanishingly unlikely but not impossible, and a
	// leftover tombstone from an interrupted run could in principle collide.
	for attempt := 0; attempt < 5; attempt++ {
		tail, err := randomTail()
		if err != nil {
			return "", err
		}
		candidate := filepath.Join(dir, base+TombstoneSuffix+tail)
		if _, err := os.Lstat(candidate); err == nil {
			continue
		}
		if err := os.Rename(path, candidate); err != nil {
			lastErr = err
			if isLocked(err) || errors.Is(err, fs.ErrNotExist) {
				return "", err
			}
			continue
		}
		return candidate, nil
	}
	if lastErr == nil {
		lastErr = errors.New("clean: could not find a free tombstone name")
	}
	return "", lastErr
}

// removeTree deletes a tombstoned directory: it lists the top-level children,
// removes each one with a bounded pool, then removes the now-empty root.
// Children are the only progress granularity a recursive removal offers, and
// running them in parallel is what makes this beat "rd /s /q".
//
// onChild, when not nil, is called from the caller's goroutine after each
// child finishes, with the number done, the total, and the child's path.
func removeTree(root string, workers int, onChild func(done, total int, current string)) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		// A plain file, or a directory that will not list: let RemoveAll try.
		return removeAll(root)
	}

	// The marker is not one of the children and is removed last, immediately
	// before the root. If it went into the pool with everything else, a
	// removal that got half way and then hit a lock would leave a tombstone
	// with no proof of who made it, which is a tombstone no sweep will ever
	// finish. It is also not progress: the user's tree has the children it
	// has, and counting Devpit's own bookkeeping file among them is a lie.
	entries = slices.DeleteFunc(entries, func(e fs.DirEntry) bool {
		return strings.EqualFold(e.Name(), TombstoneMarkerName)
	})

	total := len(entries)
	if onChild != nil {
		onChild(0, total, root)
	}

	type outcome struct {
		path string
		err  error
	}
	results := make([]outcome, total)
	if workers > total {
		workers = total
	}
	if workers > 0 {
		jobs := make(chan int)
		var wg sync.WaitGroup
		wg.Add(workers)
		for w := 0; w < workers; w++ {
			go func() {
				defer wg.Done()
				for i := range jobs {
					child := filepath.Join(root, entries[i].Name())
					results[i] = outcome{path: child, err: removeAll(child)}
				}
			}()
		}
		for i := range entries {
			jobs <- i
		}
		close(jobs)
		wg.Wait()
	}

	var errs []error
	for i, r := range results {
		if r.err != nil {
			errs = append(errs, r.err)
		}
		if onChild != nil {
			onChild(i+1, total, r.path)
		}
	}
	if len(errs) > 0 {
		// Something is still in there. Leave the marker alone: it is what
		// lets a later sweep finish what this attempt could not.
		return errors.Join(errs...)
	}
	if err := removeAll(filepath.Join(root, TombstoneMarkerName)); err != nil {
		return err
	}
	return removeAll(root)
}

// removeAll is os.RemoveAll with the long-path retry from plan.md section 7.
// Go's own RemoveAll already handles read-only files and uses POSIX delete
// semantics on Windows; the only case it cannot always reach on its own is a
// path over MAX_PATH on a machine with LongPathsEnabled set to 0.
func removeAll(path string) error {
	err := os.RemoveAll(path)
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if isLongPathError(err) {
		if long := longPath(path); long != path {
			if retry := os.RemoveAll(long); retry == nil || errors.Is(retry, fs.ErrNotExist) {
				return nil
			}
		}
	}
	return err
}
