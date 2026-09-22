// Package catalog holds Devpit's install catalog: the curated list of
// developer apps the Install screen can offer, with each app's identifier
// under every package manager Devpit knows how to drive.
//
// The catalog is data, not code: it is embedded from apps.toml at build
// time and parsed on demand, never at package init.
package catalog

import (
	_ "embed"
	"fmt"

	"github.com/pelletier/go-toml/v2"
)

//go:embed apps.toml
var appsTOML []byte

// App is one installable entry in the catalog.
type App struct {
	// Name is the human-readable app name shown in the Install screen.
	Name string `toml:"name"`
	// Category groups apps in the Install screen, for example "Runtimes"
	// or "CLI Tools".
	Category string `toml:"category"`
	// Scoop is the Scoop package name, or empty if not verified.
	Scoop string `toml:"scoop"`
	// Winget is the winget "Publisher.Id", or empty if not verified.
	Winget string `toml:"winget"`
	// Choco is the Chocolatey package id, or empty if not verified.
	Choco string `toml:"choco"`
	// Detect lists executable names used to guess whether the app is
	// already installed, independent of which manager (if any) owns it.
	Detect []string `toml:"detect"`
	// Description is a one-line summary shown under the app name.
	Description string `toml:"description"`
}

// HasManager reports whether the app has at least one known id for the
// given manager name ("scoop", "winget" or "choco").
func (a App) HasManager(name string) bool {
	switch name {
	case "scoop":
		return a.Scoop != ""
	case "winget":
		return a.Winget != ""
	case "choco":
		return a.Choco != ""
	default:
		return false
	}
}

// file mirrors the top-level shape of apps.toml.
type file struct {
	App []App `toml:"app"`
}

// Load parses the embedded catalog and returns every app, in the order
// they appear in apps.toml.
func Load() ([]App, error) {
	var f file
	if err := toml.Unmarshal(appsTOML, &f); err != nil {
		return nil, fmt.Errorf("catalog: parse apps.toml: %w", err)
	}
	return f.App, nil
}

// ByCategory loads the catalog and groups it by App.Category, preserving
// each category's app order from apps.toml.
func ByCategory() (map[string][]App, error) {
	apps, err := Load()
	if err != nil {
		return nil, err
	}
	byCat := make(map[string][]App)
	for _, a := range apps {
		byCat[a.Category] = append(byCat[a.Category], a)
	}
	return byCat, nil
}
