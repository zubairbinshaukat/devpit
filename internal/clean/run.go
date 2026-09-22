package clean

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/winapi"
)

// Run deletes every item in order and returns a report. It never returns an
// error: everything that went wrong is in Report.Skipped with a reason a
// person can act on.
//
// Cancelling ctx finishes the item in flight and stops there. Whatever was
// already deleted stays deleted, the remaining items are reported as skipped,
// and any tombstone left behind is picked up by Sweep.
//
// progress may be nil. When it is not, it is called on Run's own goroutine, so
// a callback that blocks blocks the delete; screens should forward to a
// channel and return.
func Run(ctx context.Context, items []Item, opts Options, progress func(Progress)) Report {
	started := time.Now()
	report := Report{}
	if ctx == nil {
		ctx = context.Background()
	}

	for i, it := range items {
		if err := ctx.Err(); err != nil {
			report.Skipped = append(report.Skipped, Result{
				Item:   it,
				Err:    err,
				Reason: reasonFor(it, err),
			})
			continue
		}

		report.emit(progress, Progress{Index: i, Total: len(items), Path: it.Path})
		res := deleteItem(ctx, it, opts, func(done, total int, _ string) {
			report.emit(progress, Progress{
				Index: i, Total: len(items), Path: it.Path,
				ChildDone: done, ChildTotal: total,
			})
		})

		// The tombstone rename is what frees the space, so an item whose
		// removal was interrupted afterwards still counts towards Freed; that
		// is what the user sees in Explorer.
		if res.freed {
			report.Freed += it.Size
		}
		if res.result.Err == nil {
			report.Deleted = append(report.Deleted, res.result)
		} else {
			report.Skipped = append(report.Skipped, res.result)
		}
		report.emit(progress, Progress{Index: i + 1, Total: len(items), Path: it.Path})
	}

	report.Elapsed = time.Since(started)
	return report
}

// Retry runs a single item again, for the R key on the summary screen. When a
// previous attempt left a tombstone behind, Retry finishes that instead of
// starting over, so the work already done is not repeated.
//
// Resuming is still a deletion, so it runs the pre-flight again first. The
// original path is gone by then, which is the whole point, so the checks are
// the ones that still mean something: the never-touch list and the protected
// environments, against the tombstone and against the path it came from, and
// the reparse check against the tombstone itself. The never-touch list can
// have grown since the first attempt, and an item it now covers must not be
// finished off just because the rename already happened.
func Retry(ctx context.Context, it Item, opts Options, progress func(Progress)) Result {
	if ctx == nil {
		ctx = context.Background()
	}
	if tomb, ok := findTombstoneFor(it.Path); ok {
		if err := preflightTombstone(tomb, it.Path, opts); err != nil {
			return Result{Item: it, Err: err, Reason: reasonFor(it, err)}
		}
		report := Report{}
		report.emit(progress, Progress{Index: 0, Total: 1, Path: it.Path})
		err := removeTree(tomb, opts.workers(), func(done, total int, _ string) {
			report.emit(progress, Progress{Index: 0, Total: 1, Path: it.Path, ChildDone: done, ChildTotal: total})
		})
		if err != nil {
			return lockedOrPlain(it, tomb, err)
		}
		return Result{Item: it, Reason: reasonFor(it, nil)}
	}
	rep := Run(ctx, []Item{it}, opts, progress)
	if len(rep.Deleted) == 1 {
		return rep.Deleted[0]
	}
	if len(rep.Skipped) == 1 {
		return rep.Skipped[0]
	}
	return Result{Item: it, Err: ErrGone, Reason: reasonFor(it, ErrGone)}
}

// emit calls progress with the running freed total filled in.
func (r *Report) emit(progress func(Progress), p Progress) {
	if progress == nil {
		return
	}
	p.Freed = r.Freed
	progress(p)
}

// itemOutcome is what deleteItem reports back: the result plus whether the
// item's size should count towards Freed.
type itemOutcome struct {
	result Result
	freed  bool
}

// deleteItem runs the pre-flight and then the tier's delete path, consulting
// Options.RetryLocked once if the item turns out to be locked.
func deleteItem(ctx context.Context, it Item, opts Options, onChild func(done, total int, current string)) itemOutcome {
	for {
		out := deleteItemOnce(it, opts, onChild)
		var locked *LockedError
		if out.result.Err == nil || opts.RetryLocked == nil || !errors.As(out.result.Err, &locked) {
			return out
		}
		if !opts.RetryLocked(*locked) {
			return out
		}
		if err := ctx.Err(); err != nil {
			return out
		}
	}
}

// deleteItemOnce is one attempt at one item. It takes no context: once the
// pre-flight has passed, the item in flight is always finished, so that a
// completed deletion stays completed and an interrupted one leaves a tombstone
// rather than a half-emptied folder.
func deleteItemOnce(it Item, opts Options, onChild func(done, total int, current string)) itemOutcome {
	if err := Preflight(it, opts); err != nil {
		return itemOutcome{result: Result{Item: it, Err: err, Reason: reasonFor(it, err)}}
	}
	if opts.DryRun {
		// Step 7: the pre-flight and nothing else.
		return itemOutcome{
			result: Result{Item: it, Reason: dryRunReason(it)},
			freed:  true,
		}
	}
	if it.Tier == TierSafe {
		return deletePermanently(it, opts, onChild)
	}
	return deleteToRecycleBin(it)
}

// deletePermanently is the tombstone path: rename first, report freed, then
// remove the renamed tree in parallel.
func deletePermanently(it Item, opts Options, onChild func(done, total int, current string)) itemOutcome {
	path, err := normalize(it.Path)
	if err != nil {
		return itemOutcome{result: Result{Item: it, Err: err, Reason: reasonFor(it, err)}}
	}

	tomb, err := tombstone(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			wrapped := fmt.Errorf("%w: %s", ErrGone, path)
			return itemOutcome{result: Result{Item: it, Err: wrapped, Reason: reasonFor(it, wrapped)}}
		}
		if isLocked(err) {
			// Nothing has been removed: the rename is the cheapest possible
			// lock probe, and it failed before a byte was touched.
			return itemOutcome{result: lockedOrPlain(it, "", err)}
		}
		return itemOutcome{result: Result{Item: it, Err: err, Reason: reasonFor(it, err)}}
	}

	// The space is free from here on, whatever happens next.
	if err := removeTree(tomb.Path, opts.workers(), onChild); err != nil {
		res := lockedOrPlain(it, tomb.Path, err)
		if tomb.MarkerErr != nil {
			// Say it rather than leave a folder nobody will ever finish and
			// nobody was told about.
			res.Reason += fmt.Sprintf(
				" Devpit could not mark %s as its own, so a later sweep will leave it alone; remove it by hand.",
				filepath.Base(tomb.Path),
			)
		}
		return itemOutcome{result: res, freed: true}
	}
	return itemOutcome{
		result: Result{Item: it, Reason: reasonFor(it, nil)},
		freed:  true,
	}
}

// deleteToRecycleBin is the Review and Careful path: no tombstone, the whole
// directory in one shell call so it can be restored from the Recycle Bin.
func deleteToRecycleBin(it Item) itemOutcome {
	path, err := normalize(it.Path)
	if err != nil {
		return itemOutcome{result: Result{Item: it, Err: err, Reason: reasonFor(it, err)}}
	}

	if err := checkFreeSpace(path, it.Size); err != nil {
		return itemOutcome{result: Result{Item: it, Err: err, Reason: reasonFor(it, err)}}
	}

	if err := moveToTrash(path); err != nil {
		if isLocked(err) {
			return itemOutcome{result: lockedOrPlain(it, "", err)}
		}
		return itemOutcome{result: Result{Item: it, Err: err, Reason: reasonFor(it, err)}}
	}
	return itemOutcome{
		result: Result{Item: it, Reason: reasonFor(it, nil)},
		freed:  true,
	}
}

// checkFreeSpace refuses a Recycle Bin move onto a volume that has less room
// than the item needs. The Recycle Bin keeps the bytes, so a nearly full drive
// turns the move into a failure halfway through; refusing up front and saying
// "delete it permanently instead" is the honest answer.
func checkFreeSpace(path string, size uint64) error {
	if size == 0 {
		return nil
	}
	vol := filepath.VolumeName(path)
	if vol == "" {
		vol = string(os.PathSeparator)
	} else {
		vol += string(os.PathSeparator)
	}
	space, err := winapi.FreeSpace(vol)
	if err != nil {
		// The volume would not answer. That is not a reason to refuse.
		return nil //nolint:nilerr // an unavailable free-space figure must not block a delete
	}
	if space.FreeBytes < size {
		return &InsufficientSpaceError{Path: path, Need: size, Free: space.FreeBytes}
	}
	return nil
}

// lockedOrPlain classifies a failure as a lock, with holder names when the
// Restart Manager will give them, or as itself.
func lockedOrPlain(it Item, tomb string, err error) Result {
	if !isLocked(err) {
		return Result{Item: it, Err: err, Reason: reasonFor(it, err)}
	}
	probe := tomb
	if probe == "" {
		probe = it.Path
	}
	holders := holdersFor(probe)
	locked := &LockedError{Path: it.Path, Tombstone: tomb, Holders: holders, Err: err}
	return Result{Item: it, Err: locked, Reason: reasonFor(it, locked), Holders: holders}
}

// findTombstoneFor looks for a tombstone left by an earlier attempt at path.
func findTombstoneFor(path string) (string, bool) {
	p, err := normalize(path)
	if err != nil {
		return "", false
	}
	if _, statErr := os.Lstat(p); statErr == nil {
		// The original is still there; there is nothing to finish.
		return "", false
	}
	entries, readErr := os.ReadDir(filepath.Dir(p))
	if readErr != nil {
		return "", false
	}
	prefix := filepath.Base(p) + TombstoneSuffix
	for _, e := range entries {
		if len(e.Name()) == len(prefix)+tombstoneTailLen &&
			samePath(e.Name()[:len(prefix)], prefix) &&
			IsTombstone(e.Name()) {
			return filepath.Join(filepath.Dir(p), e.Name()), true
		}
	}
	return "", false
}
