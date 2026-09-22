package clean

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/version"
)

// TombstoneMarkerName is the file Devpit writes inside a tombstone the instant
// the rename succeeds. It is what makes a tombstone self-authenticating.
//
// The name alone proves nothing: a user's own folder called
// `archive.devpit-abc12345` matches the pattern exactly, and a sweep that
// trusted the pattern would destroy it. The marker is the proof. A sweep
// removes a directory only when this file is inside it and says the directory
// used to be the sibling name the pattern implies.
const TombstoneMarkerName = ".devpit-tombstone.json"

// markerSizeLimit caps how much of a marker file is read. A real marker is a
// couple of hundred bytes; anything larger is not one, and a sweep must never
// be talked into reading a huge file by a folder that was named to look like a
// tombstone.
const markerSizeLimit = 8 << 10

// TombstoneMarker is the contents of [TombstoneMarkerName]. It records what
// the directory was before the rename, so a sweep can check the claim against
// where the directory actually sits, plus enough provenance to make a stray
// tombstone explicable to a person who finds one in Explorer.
type TombstoneMarker struct {
	// OriginalPath is the absolute path the directory had before Devpit
	// renamed it. It must be the tombstone's own directory joined with the
	// tombstone's name minus the suffix and random tail.
	OriginalPath string `json:"original_path"`
	// CreatedAt is when the rename happened, in UTC.
	CreatedAt time.Time `json:"created_at"`
	// PID is the Devpit process that did the rename.
	PID int `json:"pid"`
	// Version is that process's build version.
	Version string `json:"version"`
}

// originalPathOf returns the path a tombstone claims by its name alone: the
// same directory, with the suffix and the random tail stripped off. It is a
// pure string operation and touches no disk.
func originalPathOf(tomb string) (string, bool) {
	base := filepath.Base(tomb)
	if !IsTombstone(base) {
		return "", false
	}
	i := strings.LastIndex(base, TombstoneSuffix)
	name := base[:i]
	if name == "" {
		return "", false
	}
	return filepath.Join(filepath.Dir(tomb), name), true
}

// writeTombstoneMarker writes [TombstoneMarkerName] inside a freshly renamed
// tombstone. The caller has already renamed the directory, so a failure here
// is not a reason to stop: the in-session removal carries on, because its
// pre-flight passed before the rename. What is lost is the sweep's ability to
// finish the job later, which is why the failure is returned rather than
// swallowed, and why the doc comment on Sweep says so out loud.
func writeTombstoneMarker(tomb string) error {
	original, ok := originalPathOf(tomb)
	if !ok {
		return fmt.Errorf("clean: %s is not a tombstone name", tomb)
	}
	data, err := json.Marshal(TombstoneMarker{
		OriginalPath: original,
		CreatedAt:    time.Now().UTC(),
		PID:          os.Getpid(),
		Version:      version.Version,
	})
	if err != nil {
		return fmt.Errorf("clean: writing the tombstone marker for %s: %w", tomb, err)
	}
	if err := os.WriteFile(filepath.Join(tomb, TombstoneMarkerName), data, 0o600); err != nil {
		return fmt.Errorf("clean: writing the tombstone marker for %s: %w", tomb, err)
	}
	return nil
}

// readTombstoneMarker reads and parses the marker inside a tombstone.
func readTombstoneMarker(tomb string) (TombstoneMarker, error) {
	var m TombstoneMarker
	p := filepath.Join(tomb, TombstoneMarkerName)
	info, err := os.Lstat(p)
	if err != nil {
		return m, err
	}
	if !info.Mode().IsRegular() {
		return m, fmt.Errorf("clean: %s is not a regular file", p)
	}
	if info.Size() > markerSizeLimit {
		return m, fmt.Errorf("clean: %s is %d bytes, too large to be a marker", p, info.Size())
	}
	data, err := os.ReadFile(p) // #nosec G304 -- the path is TombstoneMarkerName inside a directory the walker found
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("clean: reading %s: %w", p, err)
	}
	return m, nil
}

// wrapNotATombstone builds the refusal that keeps a sweep away from something
// that merely looks like Devpit's work.
func wrapNotATombstone(p, why string) error {
	return fmt.Errorf("%w: %s: %s", ErrNotATombstone, p, why)
}

// verifyTombstone reports whether a directory is one Devpit made: its name
// matches the pattern, it holds a parsable marker, and that marker names the
// exact path the pattern implies. Every other directory in the world is a
// stranger's, whatever it is called, and the error says so.
func verifyTombstone(tomb string) error {
	named, ok := originalPathOf(tomb)
	if !ok {
		return fmt.Errorf("%w: %s: the name is not Devpit's", ErrNotATombstone, tomb)
	}
	want, err := normalize(named)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrNotATombstone, tomb, err)
	}
	m, err := readTombstoneMarker(tomb)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %s: there is no %s inside it", ErrNotATombstone, tomb, TombstoneMarkerName)
		}
		return fmt.Errorf("%w: %s: %w", ErrNotATombstone, tomb, err)
	}
	claimed, err := normalize(m.OriginalPath)
	if err != nil {
		return fmt.Errorf("%w: %s: the marker names no path", ErrNotATombstone, tomb)
	}
	if !samePath(claimed, want) {
		return fmt.Errorf("%w: %s: the marker claims %s, not %s", ErrNotATombstone, tomb, claimed, want)
	}
	return nil
}
