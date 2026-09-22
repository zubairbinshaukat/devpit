// Package clean is Devpit's delete engine. It turns a list of pre-measured
// items into deletions, safely: every item passes a pre-flight that refuses
// drive roots, the Windows directory, network paths, the never-touch list and
// anything reached through a reparse point; Safe items are renamed to a
// tombstone before a byte is removed, so an interrupted delete can never leave
// a half-emptied folder; Review and Careful items go to the Recycle Bin whole.
//
// The package knows nothing about the scanner or the terminal. Callers hand it
// items and a progress callback, and get a report back. It must not import
// internal/scan or any Bubble Tea package.
package clean

import (
	"errors"
	"fmt"
	"io/fs"
	"time"
)

// Tier is an item's risk tier. The numbering mirrors the scanner's so screens
// can convert with a plain cast.
type Tier int

// The three risk tiers. Safe items are deleted permanently after a tombstone
// rename; Review and Careful items go to the Recycle Bin so they are
// reversible.
const (
	TierSafe Tier = iota
	TierReview
	TierCareful
)

// String returns the tier's name as it appears in the UI.
func (t Tier) String() string {
	switch t {
	case TierSafe:
		return "Safe"
	case TierReview:
		return "Review"
	case TierCareful:
		return "Careful"
	default:
		return fmt.Sprintf("Tier(%d)", int(t))
	}
}

// Item is one thing to delete. The scanner measured Size already; the delete
// engine never walks an item to size it.
type Item struct {
	// Path is the absolute path of the directory or file to remove.
	Path string
	// Size is the pre-measured size in bytes, used for the "freed" figure.
	Size uint64
	// Tier decides how the item is removed.
	Tier Tier
	// Verify re-checks the rule that matched this path, immediately before
	// deleting it (safety rule 10). A nil Verify means there is nothing to
	// re-check.
	Verify func() error
	// RestoreHint is the one-line "how to get this back" text shown on the
	// confirmation, carried through so reports can repeat it.
	RestoreHint string
}

// Holder is a process that holds an open handle to something inside an item.
// Devpit names holders; it never terminates them.
type Holder struct {
	// PID is the operating system process identifier.
	PID uint32
	// Name is the application name the Restart Manager reports.
	Name string
}

// Options configures a run.
type Options struct {
	// NeverTouch is the list of absolute paths that are always refused,
	// matched case-insensitively as path prefixes in both directions: a path
	// inside a protected directory is refused, and so is a path that would
	// take a protected directory with it.
	NeverTouch []string
	// DryRun performs the pre-flight only and reports what would happen.
	DryRun bool
	// Workers bounds the pool that removes an item's top-level children.
	// Zero means DefaultWorkers.
	Workers int
	// RetryLocked, when set, is consulted once per locked item. Returning
	// true makes the engine try that item again; the callback is expected to
	// have asked the user to close the holder first.
	RetryLocked func(LockedError) bool
}

// DefaultWorkers is the size of the per-item removal pool when Options.Workers
// is zero. Twelve is the figure plan.md section 7 settled on.
const DefaultWorkers = 12

func (o Options) workers() int {
	if o.Workers > 0 {
		return o.Workers
	}
	return DefaultWorkers
}

// Progress reports how far a run has got. It is delivered on the goroutine
// that called Run, so a callback that blocks blocks the delete.
type Progress struct {
	// Index is the zero-based index of the item being worked on.
	Index int
	// Total is the number of items in the run.
	Total int
	// Path is the item's original path.
	Path string
	// ChildDone and ChildTotal count the item's top-level children, which is
	// the only granularity a recursive removal offers.
	ChildDone  int
	ChildTotal int
	// Freed is the running total of bytes freed so far in this run.
	Freed uint64
}

// Result is what happened to one item.
type Result struct {
	// Item is the item this result is about.
	Item
	// Err is nil when the item was deleted.
	Err error
	// Reason is the human-readable explanation, phrased for the summary
	// screen: "api\node_modules is open in Code.exe — close it and press R to
	// retry", never "1 item skipped".
	Reason string
	// Holders names the processes that held the item, when the reason was a
	// lock.
	Holders []Holder
}

// Report is the outcome of a run.
type Report struct {
	// Freed is the sum of the sizes of the items that were removed. For a Safe
	// item the space counts as freed the moment the tombstone rename succeeds,
	// which is what the UI shows. In a dry run it is what would be freed.
	Freed uint64
	// Deleted holds one result per item that was removed.
	Deleted []Result
	// Skipped holds one result per item that was not, each with a reason.
	Skipped []Result
	// Elapsed is how long the run took.
	Elapsed time.Duration
}

// Pre-flight refusals. Every one of these is a rule from docs/safety.md, and
// every one is wrapped rather than returned bare so callers can use errors.Is
// while still reading a path in the message.
var (
	// ErrEmptyPath is returned for an item with no path at all.
	ErrEmptyPath = errors.New("clean: the item has no path")
	// ErrDriveRoot refuses a drive or share root (safety rule 11).
	ErrDriveRoot = errors.New("clean: refusing a drive root")
	// ErrWindowsDir refuses anything under %WINDIR% (safety rule 11).
	ErrWindowsDir = errors.New("clean: refusing a path inside the Windows directory")
	// ErrNetworkPath refuses UNC and mapped network paths (safety rule 11).
	ErrNetworkPath = errors.New("clean: refusing a network path")
	// ErrNeverTouch refuses a path on the never-touch list (safety rule 6).
	ErrNeverTouch = errors.New("clean: refusing a path on the never-touch list")
	// ErrOwnExecutable refuses a path that would take Devpit's own executable
	// with it (safety rule 7).
	ErrOwnExecutable = errors.New("clean: refusing a path that holds Devpit's own executable")
	// ErrReparsePoint refuses a path that is, or resolves through, a reparse
	// point (safety rule 8).
	ErrReparsePoint = errors.New("clean: refusing a path that resolves through a reparse point")
	// ErrVerifyFailed means the rule that matched this path no longer matches
	// (safety rule 10).
	ErrVerifyFailed = errors.New("clean: the rule that matched this path no longer matches")
	// ErrGone means the path was already removed by something else. It wraps
	// fs.ErrNotExist so errors.Is(err, fs.ErrNotExist) works too.
	ErrGone = fmt.Errorf("clean: the path is already gone: %w", fs.ErrNotExist)
	// ErrVolumesRefused is returned by RunCommand when the arguments contain
	// --volumes (safety rule 13).
	ErrVolumesRefused = errors.New("clean: refusing to run a command with --volumes")
	// ErrNotATombstone refuses a directory that is named like one of Devpit's
	// tombstones but carries no marker proving Devpit made it. It is what
	// keeps a sweep away from a user's own `archive.devpit-abc12345`
	// (safety rules 1 and 6).
	ErrNotATombstone = errors.New("clean: refusing a folder that only looks like a tombstone")
)

// LockedError reports that an item could not be removed because something has
// it open. The tombstone, if one was created, is left in place for Sweep.
type LockedError struct {
	// Path is the item's original path.
	Path string
	// Tombstone is the renamed directory, empty when the rename itself failed.
	Tombstone string
	// Holders names the processes holding the item, when they could be found.
	Holders []Holder
	// Err is the underlying sharing or access violation.
	Err error
}

// Error implements error.
func (e *LockedError) Error() string {
	if len(e.Holders) == 0 {
		return fmt.Sprintf("clean: %s is locked by another program", e.Path)
	}
	return fmt.Sprintf("clean: %s is open in %s", e.Path, holderNames(e.Holders))
}

// Unwrap exposes the underlying operating system error.
func (e *LockedError) Unwrap() error { return e.Err }

// InsufficientSpaceError reports that the drive has less free space than the
// item needs, which is the case plan.md section 10 calls "disk fills during a
// Recycle Bin move".
type InsufficientSpaceError struct {
	// Path is the item's path.
	Path string
	// Need is the item's size in bytes.
	Need uint64
	// Free is what the volume has left.
	Free uint64
}

// Error implements error.
func (e *InsufficientSpaceError) Error() string {
	return fmt.Sprintf("clean: %s needs %d bytes in the Recycle Bin but the drive has %d free", e.Path, e.Need, e.Free)
}
