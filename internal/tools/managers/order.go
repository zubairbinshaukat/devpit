package managers

import (
	"slices"
	"strings"
)

// Upgrade-order tiers for [UpgradeOrder]; a higher tier runs later. The
// order among the tail is deliberate, from least to most disruptive to
// whatever still has to run after it.
const (
	// tierNormal is every package that affects nothing but itself.
	tierNormal = iota
	// tierGit is Git: Scoop pulls its buckets with it, and some npm
	// installs fetch git dependencies with it.
	tierGit
	// tierNode is Node.js, NVM and npm itself: replacing them mid-run
	// breaks every npm step still to come, and a Node upgrade can leave
	// the global node_modules pointing at the old version.
	tierNode
	// tierWSL is WSL: upgrading it shuts down every running distro.
	tierWSL
	// tierSelf is the package manager itself (Scoop's "scoop",
	// winget's App Installer, Chocolatey's "chocolatey", npm's "npm"):
	// every other upgrade through it has to be done before it replaces
	// its own files.
	tierSelf
	// tierDevpit is Devpit, which cannot replace its own running
	// executable cleanly and so goes after everything else (see
	// [IsDevpit]).
	tierDevpit
)

// upgradeTier places one package id in its [UpgradeOrder] tier. Ids are
// compared case-insensitively; Scoop ids may carry a "bucket/" prefix,
// which is ignored.
func upgradeTier(manager, id string) int {
	l := strings.ToLower(id)
	if i := strings.LastIndexByte(l, '/'); i >= 0 && manager == "scoop" {
		l = l[i+1:]
	}
	if IsDevpit(id) {
		return tierDevpit
	}
	switch manager {
	case "winget":
		switch {
		case l == "microsoft.appinstaller":
			return tierSelf
		case l == "microsoft.wsl":
			return tierWSL
		case strings.HasPrefix(l, "openjs.nodejs"), l == "coreybutler.nvmforwindows", l == "schniz.fnm":
			return tierNode
		case l == "git.git":
			return tierGit
		}
	case "scoop":
		switch {
		case l == "scoop":
			return tierSelf
		case l == "wsl":
			return tierWSL
		case strings.HasPrefix(l, "nodejs"), l == "nvm", l == "fnm", l == "volta":
			return tierNode
		case l == "git", l == "mingit":
			return tierGit
		}
	case "choco":
		switch {
		case l == "chocolatey":
			return tierSelf
		case l == "wsl2", l == "wsl":
			return tierWSL
		case strings.HasPrefix(l, "nodejs"), l == "nvm", l == "nvm.install":
			return tierNode
		case l == "git", l == "git.install":
			return tierGit
		}
	case "npm":
		// npm is both the manager and part of the Node toolchain; as the
		// manager it goes last.
		if l == "npm" {
			return tierSelf
		}
		if l == "corepack" {
			return tierNode
		}
	}
	return tierNormal
}

// UpgradeOrder sorts packages so that ones that could pull the rug from
// under the rest of the run go last: Git, then Node.js / NVM / npm itself,
// then WSL, then the package manager itself (Scoop's "scoop", winget's
// "Microsoft.AppInstaller", Chocolatey's "chocolatey"), and Devpit at the
// very end. Every other package keeps its relative order, as do packages
// within one tier. manager is the [Manager.Name] the packages came from.
// pkgs is not modified; a sorted copy is returned.
func UpgradeOrder(manager string, pkgs []Outdated) []Outdated {
	out := slices.Clone(pkgs)
	slices.SortStableFunc(out, func(a, b Outdated) int {
		return upgradeTier(manager, a.ID) - upgradeTier(manager, b.ID)
	})
	return out
}

// devpitIDs are the package ids Devpit is published under: "devpit" in the
// Scoop bucket .goreleaser.yml publishes to (the goreleaser project_name).
// Devpit has no winget manifest yet, so a winget id is recognised by
// [IsDevpit]'s segment match instead.
var devpitIDs = []string{"devpit"}

// IsDevpit reports whether id is Devpit itself, in any manager: the Scoop
// app "devpit" (with or without its "bucket/" prefix), or any id with a
// "devpit" segment, case-insensitively, such as a future winget
// "ZubairBinShaukat.Devpit". The Update screen uses it to keep Devpit from
// trying to replace its own running executable mid-run.
func IsDevpit(id string) bool {
	l := strings.ToLower(strings.TrimSpace(id))
	if slices.Contains(devpitIDs, l) {
		return true
	}
	for _, seg := range strings.FieldsFunc(l, func(r rune) bool { return r == '.' || r == '/' || r == '\\' }) {
		if seg == "devpit" {
			return true
		}
	}
	return false
}
