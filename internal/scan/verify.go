package scan

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Verify re-checks an item immediately before anything is deleted.
//
// A scan result is a photograph of a moving filesystem. Between the scan and
// the confirmation the user may have renamed the folder, reinstalled the
// project, closed the editor that was holding it, or deleted it in Explorer.
// The delete engine calls Verify on every item it is about to touch and
// refuses the ones that fail, which is what keeps safety rule 10 from being a
// comment in a design document.
//
// Verify checks, in order, that the path is not protected, still exists, is
// still the kind of thing it was, is not and does not resolve through a
// reparse point, still has the name it had, and still has one of the marker
// files that made it junk in the first place. An item the scan already
// flagged as unverified is not asked for a marker it never had.
//
// It returns nil when the item is still safe to delete, and one of ErrGone,
// ErrNotADirectory, ErrReparsePoint, ErrRenamed, ErrMarkerMissing or
// ErrProtectedPath otherwise, wrapped with the path for display.
func Verify(it Item) error {
	path := normalizePath(it.Path)
	if path == "" || !filepath.IsAbs(path) {
		return fmt.Errorf("scan: %q: %w", it.Path, ErrProtectedPath)
	}
	if isDriveRoot(path) || pathIsUNC(path) || isRemoteDrive(path) {
		return fmt.Errorf("scan: %s: %w", path, ErrProtectedPath)
	}
	if protectedByEnvironment(path) {
		return fmt.Errorf("scan: %s: %w", path, ErrProtectedPath)
	}

	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("scan: %s: %w", path, ErrGone)
		}
		return fmt.Errorf("scan: checking %s: %w", path, err)
	}

	if isLeafMode(info.Mode()) || isReparseInfo(info) {
		return fmt.Errorf("scan: %s: %w", path, ErrReparsePoint)
	}
	through, err := ResolvesThroughReparse(filepath.Dir(path))
	if err == nil && through {
		return fmt.Errorf("scan: %s: %w", path, ErrReparsePoint)
	}

	wantDir := it.Kind != KindLargeFile
	if wantDir != info.IsDir() {
		return fmt.Errorf("scan: %s: %w", path, ErrNotADirectory)
	}

	if it.Name != "" && !strings.EqualFold(filepath.Base(path), it.Name) {
		return fmt.Errorf("scan: %s: %w", path, ErrRenamed)
	}

	if len(it.Markers) > 0 && !it.Unverified {
		probe := Rule{Markers: it.Markers, MarkersInside: it.MarkersInside}
		if !probe.HasMarker(path) {
			return fmt.Errorf("scan: %s: %w", path, ErrMarkerMissing)
		}
	}
	return nil
}

// protectedByEnvironment reports whether a path sits inside the Windows
// directory or a Program Files tree. Those are never Devpit's to delete, and
// the check is repeated here rather than borrowed from the scan's filters so
// that a delete cannot inherit a scan's looser configuration.
func protectedByEnvironment(path string) bool {
	for _, env := range []string{"WINDIR", "SystemRoot", "ProgramFiles", "ProgramFiles(x86)", "ProgramW6432"} {
		v := os.Getenv(env)
		if v == "" {
			continue
		}
		if underPath(path, normalizePath(v)) {
			return true
		}
	}
	return false
}
