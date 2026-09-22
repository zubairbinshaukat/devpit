// Package wt finds Windows Terminal's settings.json across every install
// flavor and patches its font fallback list so glyphs from Devpit's icon
// font (internal/fonts) render (plan.md section 5, step 5).
//
// The patcher operates purely on bytes and the github.com/tailscale/hujson
// AST: it has no Windows-specific dependency, so it (and its tests) run
// unmodified on any GOOS.
package wt

import (
	"os"
	"path/filepath"
)

// Flavor values returned in Location.Flavor.
const (
	FlavorStable     = "stable"
	FlavorPreview    = "preview"
	FlavorUnpackaged = "unpackaged"
)

// Location is one Windows Terminal settings.json found on disk.
type Location struct {
	Path   string
	Flavor string
}

// FindOptions configures FindSettings. The zero value uses the real
// %LOCALAPPDATA%.
type FindOptions struct {
	// LocalAppData overrides %LOCALAPPDATA% so tests can point
	// FindSettings at a fake directory tree instead of the real one.
	LocalAppData string
}

// FindSettings returns every Windows Terminal settings.json that exists on
// disk, across the Store (stable), Store (Preview) and unpackaged/portable
// install flavors. Two installs (e.g. stable and Preview side by side) are
// both returned so callers can patch both (plan.md section 10: "Two
// Windows Terminal installs -> patch both").
func FindSettings(opts FindOptions) []Location {
	base := opts.LocalAppData
	if base == "" {
		base = os.Getenv("LOCALAPPDATA")
	}

	candidates := []Location{
		{
			Path: filepath.Join(base, "Packages", "Microsoft.WindowsTerminal_8wekyb3d8bbwe",
				"LocalState", "settings.json"),
			Flavor: FlavorStable,
		},
		{
			Path: filepath.Join(base, "Packages", "Microsoft.WindowsTerminalPreview_8wekyb3d8bbwe",
				"LocalState", "settings.json"),
			Flavor: FlavorPreview,
		},
		{
			Path:   filepath.Join(base, "Microsoft", "Windows Terminal", "settings.json"),
			Flavor: FlavorUnpackaged,
		},
	}

	found := make([]Location, 0, len(candidates))
	for _, c := range candidates {
		info, err := os.Stat(c.Path)
		if err == nil && !info.IsDir() {
			found = append(found, c)
		}
	}
	return found
}
