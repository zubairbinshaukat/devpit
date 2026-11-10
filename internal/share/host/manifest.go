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

// manifestVersion is bumped when the file's shape changes. Version 2 added
// the hotspot fields; a version 1 file is still read (its hotspot fields are
// simply empty).
const manifestVersion = 2

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

	// The hotspot record (version 2). It holds names and flags only: there
	// is no field for a hotspot password, the temporary one or the user's.

	// Mode is [ModeHotspot] for a share over a direct hotspot.
	Mode Mode `json:"mode,omitempty"`
	// HotspotConfig is "saved" or "temp", as sent to the worker.
	HotspotConfig string `json:"hotspot_config,omitempty"`
	// HotspotSSID is the running hotspot's name, which cleanup checks before
	// it stops anything. A name is not a secret.
	HotspotSSID string `json:"hotspot_ssid,omitempty"`
	// HotspotRule is the devpit-hs- firewall rule, recorded before it exists.
	HotspotRule string `json:"hotspot_rule,omitempty"`
	// HotspotStarted says Devpit started the hotspot (recorded before the
	// start, cleared when Windows says it did not start or it was already on).
	HotspotStarted bool `json:"hotspot_started,omitempty"`
	// HotspotTimeoutWasOn and HotspotTimeoutChanged say the "turn off when
	// nothing is connected" setting was on and Devpit turned it off.
	HotspotTimeoutWasOn   bool `json:"hotspot_timeout_was_on,omitempty"`
	HotspotTimeoutChanged bool `json:"hotspot_timeout_changed,omitempty"`
	// HotspotLegacy says a temporary start overwrote the saved hotspot
	// settings (Windows before 26100).
	HotspotLegacy bool `json:"hotspot_legacy,omitempty"`

	// file is where the manifest was read from, when it was.
	file string
}

// clearHotspot forgets the hotspot record, once the worker has undone it.
func (m *Manifest) clearHotspot() {
	m.Mode, m.HotspotConfig, m.HotspotSSID, m.HotspotRule = ModeNormal, "", "", ""
	m.HotspotStarted, m.HotspotTimeoutWasOn, m.HotspotTimeoutChanged, m.HotspotLegacy = false, false, false, false
}

// MsgHotspotSettingsLost is what to tell the person after cleaning up a
// leftover whose temporary hotspot overwrote their saved hotspot settings
// (Windows before 26100): Devpit held the old ones in memory only, so they
// are gone with the run that died (decision 14).
const MsgHotspotSettingsLost = "Your hotspot name and password were changed by Devpit and could not be put back. " +
	"Set them again in Settings > Network & internet > Mobile hotspot."

// HotspotNote is a line to show with this leftover, or "" for none: today
// [MsgHotspotSettingsLost] when a temporary start overwrote the saved
// hotspot settings. It is shown whether or not the cleanup succeeds, since
// nothing can put those settings back.
func (m Manifest) HotspotNote() string {
	if m.HotspotLegacy {
		return MsgHotspotSettingsLost
	}
	return ""
}

// request turns the manifest into the worker's cleanup request.
func (m Manifest) request() elevate.ShareRequest { return m.cleanupRequest(bootTime()) }

// cleanupRequest is request with the time Windows last started. A share set
// up before then had its hotspot turned off by the restart, so a hotspot on
// now is not Devpit's: the request then asks for no stop (the rule and the
// timeout are still put back). An unknown boot time or start time changes
// nothing; the worker's own name check still applies.
func (m Manifest) cleanupRequest(boot time.Time) elevate.ShareRequest {
	r := elevate.ShareRequest{
		Op: elevate.ShareOpCleanup, User: m.User, SID: m.SID, Name: m.Share,
		Index: m.ProfileIndex, Original: m.ProfileOriginal, Rules: m.Rules,
		HotspotConfig: m.HotspotConfig, HotspotSSID: m.HotspotSSID, HotspotRule: m.HotspotRule,
		HotspotStarted: m.HotspotStarted, HotspotTimeoutWasOn: m.HotspotTimeoutWasOn,
		HotspotTimeoutChanged: m.HotspotTimeoutChanged, HotspotLegacy: m.HotspotLegacy,
	}
	if m.Granted {
		r.Path = m.Path
	}
	if r.HotspotStarted && !boot.IsZero() && !m.StartedAt.IsZero() && m.StartedAt.Before(boot) {
		r.HotspotStarted = false
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
