package rules

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/scan"
)

// ScoopRoot returns the Scoop installation directory: %SCOOP% when it is set,
// otherwise %USERPROFILE%\scoop. It returns an empty string when neither can
// be determined.
func ScoopRoot() string {
	if v := os.Getenv("SCOOP"); v != "" {
		return filepath.Clean(v)
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "scoop")
	}
	return ""
}

// ScoopCurrentVersion returns the version directory that an app's "current"
// junction points at.
//
// Scoop keeps every installed version of an app side by side and makes
// "current" a junction to the one in use. Reading that junction is the only
// honest way to know which version is live, so when the junction cannot be
// read the answer is false and the caller lists nothing for that app. An
// unreadable junction must never be treated as "no current version": that
// would offer to delete the version the user is running.
func ScoopCurrentVersion(appDir string) (string, bool) {
	target, err := os.Readlink(filepath.Join(appDir, "current"))
	if err != nil {
		return "", false
	}
	name := filepath.Base(filepath.Clean(strings.TrimSuffix(target, string(filepath.Separator))))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "", false
	}
	return name, true
}

// ScoopOldVersionDirs returns the superseded version directories under a
// Scoop root: everything in scoop\apps\<app>\ that is neither the "current"
// junction nor the version that junction points at.
//
// Two things are never in the result, and a test pins both. The "current"
// junction itself is never returned, because deleting a junction Scoop
// created would leave the shims pointing at nothing. The persist directory is
// never returned, because it holds the app's own saved data — configuration,
// profiles, databases — and it is a sibling of apps, not a version of one.
func ScoopOldVersionDirs(scoopRoot string) []string {
	if scoopRoot == "" {
		return nil
	}
	appsDir := filepath.Join(scoopRoot, "apps")
	apps, err := os.ReadDir(appsDir)
	if err != nil {
		return nil
	}

	var out []string
	for _, app := range apps {
		if !app.IsDir() {
			continue
		}
		appDir := filepath.Join(appsDir, app.Name())
		current, ok := ScoopCurrentVersion(appDir)
		if !ok {
			// Cannot tell which version is live, so offer none of them.
			continue
		}
		versions, verr := os.ReadDir(appDir)
		if verr != nil {
			continue
		}
		for _, version := range versions {
			name := version.Name()
			if strings.EqualFold(name, "current") || strings.EqualFold(name, current) {
				continue
			}
			info, ierr := version.Info()
			if ierr != nil || !info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
				continue
			}
			out = append(out, filepath.Join(appDir, name))
		}
	}
	sort.Strings(out)
	return out
}

// Scoop returns the Scoop rules for the Scoop root this machine uses.
func Scoop() []scan.Rule { return ScoopRules(ScoopRoot()) }

// ScoopRules returns the Scoop rules for an explicit root, which is what the
// tests use.
func ScoopRules(scoopRoot string) []scan.Rule {
	out := []scan.Rule{
		{
			Name:        "scoop download cache",
			Kind:        scan.KindScoop,
			Locations:   []string{`%SCOOP%\cache`, `%USERPROFILE%\scoop\cache`},
			Tier:        scan.TierSafe,
			RestoreHint: "Nothing to do; Scoop downloads an installer again the next time it needs one.",
			Description: "Installers Scoop has already used.",
		},
	}

	if old := ScoopOldVersionDirs(scoopRoot); len(old) > 0 {
		out = append(out, scan.Rule{
			Name:        "scoop old versions",
			Kind:        scan.KindScoop,
			Locations:   old,
			Tier:        scan.TierReview,
			RestoreHint: "Nothing breaks; the version in use is untouched. Run scoop install <app>@<version> if you need an old one back.",
			Description: "Superseded app versions Scoop kept after an update.",
		})
	}
	return out
}
