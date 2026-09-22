// Package managers drives the package managers Devpit knows how to detect
// and use: Scoop, winget, Chocolatey and npm's global installs. Each
// implementation only builds argv slices and parses text a caller already
// captured; nothing in this package execs a process itself. That is left to
// [tools.RunStep] on the Update and Install screens, so parsing stays pure
// and testable without ever running a real package manager.
package managers

import "github.com/zubairbinshaukat/devpit/internal/tools"

// Installed describes one package a manager reports as already installed.
type Installed struct {
	// Name is the display name, if the manager's output distinguishes one
	// from an id; otherwise it equals ID.
	Name string
	// ID is the manager-specific package identifier (a winget "Id", a
	// Scoop app name, a Chocolatey id, an npm package name).
	ID string
	// Version is the installed version string, as reported verbatim.
	Version string
}

// Outdated describes one package a manager reports as having an update
// available.
type Outdated struct {
	// Name is the display name, if the manager's output distinguishes one
	// from an id; otherwise it equals ID.
	Name string
	// ID is the manager-specific package identifier.
	ID string
	// Current is the installed version.
	Current string
	// Latest is the version available to upgrade to.
	Latest string
}

// Manager is one package manager Devpit can list, check and drive updates
// and installs through. Every method that returns a command is pure: it
// builds an argv slice for a caller to run with [tools.RunStep], never runs
// anything itself.
type Manager interface {
	// Name is the manager's short identifier: "scoop", "winget", "choco"
	// or "npm".
	Name() string
	// ListCmd is the argv that lists installed packages.
	ListCmd() []string
	// OutdatedCmd is the argv that lists packages with an update
	// available.
	OutdatedCmd() []string
	// UpgradeAllCmds is the ordered sequence of argv steps that upgrade
	// every package this manager owns. Manager-specific: Scoop and npm
	// need several steps (update, then cleanup); winget and Chocolatey
	// need one.
	UpgradeAllCmds() [][]string
	// InstallCmd is the argv that installs the given manager-specific
	// package id.
	InstallCmd(id string) []string
	// ParseList parses ListCmd's captured output into installed packages.
	ParseList(out string) []Installed
	// ParseOutdated parses OutdatedCmd's captured output into packages
	// with an update available.
	ParseOutdated(out string) []Outdated
	// NeedsElevation reports whether this manager's commands must run
	// through the elevated worker (true for Chocolatey by default).
	NeedsElevation() bool
}

// All returns one instance of every manager Devpit supports, in the same
// preference order used by [Preferred]: scoop, winget, choco, npm.
func All() []Manager {
	return []Manager{Scoop{}, Winget{}, Choco{}, NPM{}}
}

// byName looks a manager up by its [Manager.Name], or returns nil.
func byName(name string) Manager {
	for _, m := range All() {
		if m.Name() == name {
			return m
		}
	}
	return nil
}

// Preferred picks which manager the Install and Update screens should use.
// override, when non-empty, wins outright as long as it names a known
// manager. Otherwise it prefers whichever of scoop, winget or choco was
// found in detected, in that order (npm is never picked as the primary
// manager; it always runs as its own Update step). Preferred returns nil if
// nothing matches.
func Preferred(detected []tools.Tool, override string) Manager {
	if override != "" {
		if m := byName(override); m != nil {
			return m
		}
	}

	found := make(map[string]bool, len(detected))
	for _, t := range detected {
		if t.Found {
			found[t.Name] = true
		}
	}

	for _, name := range []string{"scoop", "winget", "choco"} {
		if found[name] {
			return byName(name)
		}
	}
	return nil
}
