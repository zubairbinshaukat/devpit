package clean

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// Preflight runs every refusal check for one item and returns nil only when
// the item may be deleted. It is the second, independent enforcement point for
// the never-touch list (the scanner's root filter is the first), and the only
// one for drive roots, %WINDIR%, network paths, Devpit's own executable,
// reparse points and the rule re-verification.
//
// Preflight never modifies anything. Run calls it for every item, including in
// a dry run, and callers may call it on its own to grey out a row.
func Preflight(it Item, opts Options) error {
	p, err := normalize(it.Path)
	if err != nil {
		return err
	}

	if err := checkEnvironment(p); err != nil {
		return err
	}
	if err := checkNeverTouch(p, opts); err != nil {
		return err
	}
	if err := checkExists(p); err != nil {
		return err
	}
	if err := checkReparse(p); err != nil {
		return err
	}

	// Rule 10: the marker file is re-verified immediately before deleting.
	if it.Verify != nil {
		if verifyErr := it.Verify(); verifyErr != nil {
			return fmt.Errorf("%w: %s: %w", ErrVerifyFailed, p, verifyErr)
		}
	}
	return nil
}

// preflightTombstone is the pre-flight for finishing a tombstone that an
// earlier run left behind, used by both Sweep and Retry. Resuming a deletion
// is still a deletion, and the rename having already happened is not a licence
// to skip the checks.
//
// What it can check is a subset of [Preflight]'s, and deliberately so. The
// original path no longer exists, so the marker re-verification (rule 10) has
// nothing left to look at; the rule matched before the rename, and the
// directory that matched it is the one now sitting under the tombstone name.
// Everything else still applies, and applies to both names: the never-touch
// list and the protected environments are checked against the tombstone and
// against the path it was renamed from, because a list that now covers either
// one covers this directory. The reparse check runs on the tombstone, which is
// the thing about to be removed.
//
// original may be empty, in which case it is derived from the tombstone's own
// name.
func preflightTombstone(tomb, original string, opts Options) error {
	t, err := normalize(tomb)
	if err != nil {
		return err
	}
	if original == "" {
		named, ok := originalPathOf(t)
		if !ok {
			return fmt.Errorf("%w: %s: the name is not Devpit's", ErrNotATombstone, t)
		}
		original = named
	}
	o, err := normalize(original)
	if err != nil {
		return err
	}

	for _, p := range []string{t, o} {
		if err := checkEnvironment(p); err != nil {
			return err
		}
		if err := checkNeverTouch(p, opts); err != nil {
			return err
		}
	}
	if err := checkExists(t); err != nil {
		return err
	}
	return checkReparse(t)
}

// checkEnvironment is rule 11: drive and share roots, the Windows directory,
// and network paths, none of which Devpit ever deletes from.
func checkEnvironment(p string) error {
	if isDriveRoot(p) {
		return fmt.Errorf("%w: %s", ErrDriveRoot, p)
	}
	if win := windowsDir(); win != "" && within(p, win) {
		return fmt.Errorf("%w: %s", ErrWindowsDir, p)
	}
	if isUNC(p) || isNetworkPath(p) {
		return fmt.Errorf("%w: %s", ErrNetworkPath, p)
	}
	return nil
}

// checkNeverTouch is rules 6 and 7: the user's never-touch list, and Devpit's
// own executable, which is on that list whether the user put it there or not.
func checkNeverTouch(p string, opts Options) error {
	for _, entry := range opts.NeverTouch {
		guarded, normErr := normalize(entry)
		if normErr != nil {
			continue
		}
		// Both directions: a path inside a protected directory is refused, and
		// so is a path that would take a protected directory down with it.
		if within(p, guarded) || within(guarded, p) {
			return fmt.Errorf("%w: %s", ErrNeverTouch, guarded)
		}
	}
	if exe := ownExecutable(); exe != "" && within(exe, p) {
		return fmt.Errorf("%w: %s", ErrOwnExecutable, exe)
	}
	return nil
}

// checkExists reports the path as gone rather than as a failure. Something
// else removing it first is an ordinary outcome, but it is not a deletion.
func checkExists(p string) error {
	if _, statErr := os.Lstat(p); statErr != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrGone, p)
		}
		return statErr
	}
	return nil
}

// checkReparse is rule 8: never follow a reparse point, and never delete
// through one.
func checkReparse(p string) error {
	through, reparseErr := resolvesThroughReparse(p)
	if reparseErr != nil {
		return fmt.Errorf("clean: checking %s for reparse points: %w", p, reparseErr)
	}
	if through {
		return fmt.Errorf("%w: %s", ErrReparsePoint, p)
	}
	return nil
}
