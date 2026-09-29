package host

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/elevate"
	"github.com/zubairbinshaukat/devpit/internal/share/store"
)

// manifestPrefix and manifestExt make up a manifest's file name inside the
// share directory: share-host-<account>.json. Every share has its own file, so
// two Devpit windows that share at the same time never overwrite or delete
// each other's record. share-host.json, the one name earlier builds used, is
// still read.
const (
	manifestPrefix = "share-host"
	manifestExt    = ".json"
)

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
	// Granted says the folder permission step was reached, so cleanup has a
	// permission to take off the folder. Before it, the account may not even
	// exist, and icacls fails on a name it cannot find, which made the cleanup
	// of a run that died early fail every time it was tried.
	Granted bool `json:"granted,omitempty"`
	// Share is the share name.
	Share string `json:"share"`
	// ProfileIndex and ProfileOriginal record a network category Devpit
	// changed and what to put it back to. Zero and empty mean unchanged.
	ProfileIndex    uint32 `json:"profile_index,omitempty"`
	ProfileOriginal string `json:"profile_original,omitempty"`
	// Rules are the firewall rules Devpit turned on.
	Rules []string `json:"rules,omitempty"`

	// file is where the manifest was read from, when it was.
	file string
}

// request turns the manifest into the worker's cleanup request.
func (m Manifest) request() elevate.ShareRequest {
	r := elevate.ShareRequest{
		Op: elevate.ShareOpCleanup, User: m.User, SID: m.SID, Name: m.Share,
		Index: m.ProfileIndex, Original: m.ProfileOriginal, Rules: m.Rules,
	}
	if m.Granted {
		r.Path = m.Path
	}
	return r
}

// manifestPath is where the manifest of the share made with account user
// lives under dir.
func manifestPath(dir, user string) string {
	return filepath.Join(dir, manifestPrefix+"-"+user+manifestExt)
}

// saveManifest writes the manifest atomically.
func saveManifest(dir string, m Manifest) error {
	m.Version = manifestVersion
	return store.WriteJSON(manifestPath(dir, m.User), m)
}

// removeManifest deletes the file of m: the one it was read from, and the one
// its account name gives.
func removeManifest(dir string, m Manifest) error {
	var errs []error
	if m.file != "" {
		errs = append(errs, store.Remove(m.file))
	}
	if m.User != "" {
		errs = append(errs, store.Remove(manifestPath(dir, m.User)))
	}
	return errors.Join(errs...)
}

// LoadManifests reads every manifest in dir, oldest share first. A damaged
// file is an error, and is left in place for the user to look at.
func LoadManifests(dir string) ([]Manifest, error) {
	paths, err := filepath.Glob(filepath.Join(dir, manifestPrefix+"*"+manifestExt))
	if err != nil {
		return nil, err
	}
	var out []Manifest
	for _, p := range paths {
		if strings.Contains(filepath.Base(p), ".tmp-") {
			continue
		}
		var m Manifest
		if err := store.ReadJSON(p, &m); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue // removed while we looked
			}
			return nil, err
		}
		m.file = p
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out, nil
}

// LoadManifest returns the oldest manifest in dir, and reports false when
// there is none.
func LoadManifest(dir string) (Manifest, bool, error) {
	all, err := LoadManifests(dir)
	if err != nil || len(all) == 0 {
		return Manifest{}, false, err
	}
	return all[0], true, nil
}

// Alive reports whether the process that wrote a manifest is still running:
// the PID is running and that process was started before the share was.
// Windows reuses PIDs quickly, so a PID alone would make an unrelated program
// that got the same number keep a dead share from ever being cleaned up.
type Alive func(pid int, startedAt time.Time) bool

// Leftover looks for a share an earlier run left behind: the oldest manifest
// whose process is gone. alive says whether its process still runs, and self
// is this process, whose own shares are never leftovers.
func Leftover(dir string, alive Alive, self int) (Manifest, bool, error) {
	all, err := LoadManifests(dir)
	if err != nil {
		return Manifest{}, false, err
	}
	for _, m := range all {
		if m.PID == self || (m.PID != 0 && alive != nil && alive(m.PID, m.StartedAt)) {
			continue
		}
		return m, true, nil
	}
	return Manifest{}, false, nil
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
	return removeManifest(dir, m)
}

// ProcessAlive is the real [Alive]: the PID is running, and, when startedAt
// is known, the process running under it was created no later than that.
func ProcessAlive(pid int, startedAt time.Time) bool { return processAlive(pid, startedAt) }
