package host

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/elevate"
	"github.com/zubairbinshaukat/devpit/internal/share/store"
)

// manifestFile is the name of the manifest inside the share directory.
const manifestFile = "share-host.json"

// manifestVersion is bumped when the file's shape changes.
const manifestVersion = 1

// Manifest is what a share created, saved to disk as it is created. It is
// what the next launch reads to find and remove leftovers when Devpit and its
// elevated worker both died before cleaning up.
//
// It holds names, paths and identifiers only. It never holds the password: a
// leftover account cannot be used without it, expires by itself after
// [elevate.AccountLifetime], and is removed by name.
type Manifest struct {
	// Version is the file format.
	Version int `json:"version"`
	// PID is the Devpit process that owns the share, so a second Devpit does
	// not clean up a share that is alive in the first.
	PID int `json:"pid"`
	// StartedAt is when the share was set up.
	StartedAt time.Time `json:"started_at"`
	// User is the temporary account, devpit-xxxx.
	User string `json:"user"`
	// SID is the account's security identifier, once known.
	SID string `json:"sid,omitempty"`
	// Path is the shared folder.
	Path string `json:"path"`
	// Share is the share name.
	Share string `json:"share"`
	// ProfileIndex and ProfileOriginal record a network category Devpit
	// changed and what to put it back to. Zero and empty mean unchanged.
	ProfileIndex    uint32 `json:"profile_index,omitempty"`
	ProfileOriginal string `json:"profile_original,omitempty"`
	// Rules are the firewall rules Devpit turned on.
	Rules []string `json:"rules,omitempty"`
}

// request turns the manifest into the worker's cleanup request.
func (m Manifest) request() elevate.ShareRequest {
	return elevate.ShareRequest{
		Op: elevate.ShareOpCleanup, User: m.User, SID: m.SID, Path: m.Path, Name: m.Share,
		Index: m.ProfileIndex, Original: m.ProfileOriginal, Rules: m.Rules,
	}
}

// manifestPath is where the manifest lives under dir.
func manifestPath(dir string) string { return filepath.Join(dir, manifestFile) }

// saveManifest writes the manifest atomically.
func saveManifest(dir string, m Manifest) error {
	m.Version = manifestVersion
	return store.WriteJSON(manifestPath(dir), m)
}

// LoadManifest reads the manifest, and reports false when there is none. A
// damaged file is an error, and is left in place for the user to look at.
func LoadManifest(dir string) (Manifest, bool, error) {
	var m Manifest
	err := store.ReadJSON(manifestPath(dir), &m)
	if errors.Is(err, os.ErrNotExist) {
		return Manifest{}, false, nil
	}
	if err != nil {
		return Manifest{}, false, err
	}
	return m, true, nil
}

// Leftover looks for a share an earlier run left behind. It returns the
// manifest only when the process that wrote it is gone: alive says whether a
// PID is still running, and self is this process, whose own share is never a
// leftover.
func Leftover(dir string, alive func(pid int) bool, self int) (Manifest, bool, error) {
	m, ok, err := LoadManifest(dir)
	if err != nil || !ok {
		return Manifest{}, false, err
	}
	if m.PID == self || (m.PID != 0 && alive != nil && alive(m.PID)) {
		return Manifest{}, false, nil
	}
	return m, true, nil
}

// CleanUp removes what a leftover manifest describes: it starts the elevated
// worker (one UAC prompt), asks it to undo the manifest, and deletes the
// manifest once the worker says everything is gone. If the worker cannot
// finish, the manifest stays, so the next launch offers again.
func CleanUp(ctx context.Context, dir string, m Manifest, launch Launcher) error {
	admin, err := launch(ctx)
	if err != nil {
		return err
	}
	defer admin.Close() //nolint:errcheck // the worker is done either way
	if err := admin.Share(ctx, m.request(), nil); err != nil {
		return fmt.Errorf("cleaning up the old share: %w", err)
	}
	return store.Remove(manifestPath(dir))
}

// ProcessAlive reports whether a process with this PID is running. It is the
// real check for [Leftover].
func ProcessAlive(pid int) bool { return processAlive(pid) }
