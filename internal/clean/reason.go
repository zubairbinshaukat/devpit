package clean

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
)

// shortPath is what reasons name an item by: the last two components, which is
// enough to tell "api\node_modules" from "web\node_modules" without printing a
// path that overflows the summary line.
func shortPath(p string) string {
	if p == "" {
		return "the item"
	}
	dir, base := filepath.Split(filepath.Clean(p))
	parent := filepath.Base(filepath.Clean(dir))
	if parent == "." || parent == string(filepath.Separator) || parent == base || dir == "" {
		return base
	}
	return filepath.Join(parent, base)
}

// reasonFor turns an error into the sentence docs/safety.md asks for: what
// happened, to what, and what to do about it. Nothing fails silently and
// nothing is reported as "3 items skipped".
func reasonFor(it Item, err error) string {
	name := shortPath(it.Path)

	var locked *LockedError
	if errors.As(err, &locked) {
		if len(locked.Holders) == 0 {
			return fmt.Sprintf("Couldn't delete %s — another program has it open. Close it and press R to retry.", name)
		}
		return fmt.Sprintf("Couldn't delete %s — it's open in %s. Close it and press R to retry.", name, holderNames(locked.Holders))
	}

	var space *InsufficientSpaceError
	if errors.As(err, &space) {
		return fmt.Sprintf(
			"Couldn't move %s to the Recycle Bin — it needs %s and the drive has %s free. Delete it permanently instead, or empty the Recycle Bin.",
			name, humanBytes(space.Need), humanBytes(space.Free),
		)
	}

	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return fmt.Sprintf("Stopped before %s — nothing was removed. Run the clean again to finish.", name)
	case errors.Is(err, ErrGone):
		return fmt.Sprintf("%s was already gone — something else removed it.", name)
	case errors.Is(err, ErrDriveRoot):
		return fmt.Sprintf("Refused %s — that's a drive root, and Devpit never deletes one.", name)
	case errors.Is(err, ErrWindowsDir):
		return fmt.Sprintf("Refused %s — it's inside the Windows directory.", name)
	case errors.Is(err, ErrNetworkPath):
		return fmt.Sprintf("Refused %s — it's on a network drive, and Devpit only cleans local disks.", name)
	case errors.Is(err, ErrNeverTouch):
		return fmt.Sprintf("Skipped %s — it's on your never-touch list. Settings → Never touch to change that.", name)
	case errors.Is(err, ErrOwnExecutable):
		return fmt.Sprintf("Refused %s — Devpit's own program file lives in there.", name)
	case errors.Is(err, ErrReparsePoint):
		return fmt.Sprintf("Refused %s — it's a junction or symlink, or it sits under one, and Devpit never deletes through a link.", name)
	case errors.Is(err, ErrVerifyFailed):
		return fmt.Sprintf("Skipped %s — it no longer matches the rule that found it. Rescan and look again.", name)
	case errors.Is(err, ErrNotATombstone):
		return fmt.Sprintf("Left %s alone — it's named like one of Devpit's leftovers but Devpit didn't make it, so nothing was removed.", name)
	case errors.Is(err, ErrEmptyPath):
		return "Skipped an item with no path."
	case err != nil:
		return fmt.Sprintf("Couldn't delete %s — %v.", name, err)
	}

	if it.Tier == TierSafe {
		return fmt.Sprintf("Deleted %s.", name)
	}
	return fmt.Sprintf("Moved %s to the Recycle Bin.", name)
}

// dryRunReason describes what a run without DryRun would have done.
func dryRunReason(it Item) string {
	name := shortPath(it.Path)
	if it.Tier == TierSafe {
		return fmt.Sprintf("Would delete %s permanently, freeing %s.", name, humanBytes(it.Size))
	}
	return fmt.Sprintf("Would move %s to the Recycle Bin, freeing %s.", name, humanBytes(it.Size))
}

// humanBytes formats a byte count the way the summary card does. It uses the
// 1024-based steps with the short names everyone reads as such.
func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for v := n / unit; v >= unit && exp < 4; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTP"[exp])
}
