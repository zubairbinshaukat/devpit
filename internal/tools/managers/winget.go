package managers

import (
	"regexp"
	"strconv"
	"strings"
)

// Winget drives the winget package manager. Installs and updates run as the
// current user; each package prompts for UAC individually unless the
// caller is already elevated.
type Winget struct{}

// Name implements [Manager].
func (Winget) Name() string { return "winget" }

// ListCmd implements [Manager].
func (Winget) ListCmd() []string {
	return []string{"winget", "list", "--accept-source-agreements"}
}

// OutdatedCmd implements [Manager].
func (Winget) OutdatedCmd() []string {
	return []string{"winget", "upgrade", "--accept-source-agreements"}
}

// UpgradeAllCmds implements [Manager]. A single step covers every package;
// --disable-interactivity keeps a package's own installer from popping a
// prompt Devpit can't answer.
func (Winget) UpgradeAllCmds() [][]string {
	return [][]string{
		{"winget", "upgrade", "--all", "--accept-source-agreements", "--disable-interactivity"},
	}
}

// InstallCmd implements [Manager]. id is a winget "Publisher.Id".
func (Winget) InstallCmd(id string) []string {
	return []string{
		"winget", "install", "--id", id, "-e",
		"--accept-source-agreements", "--accept-package-agreements",
	}
}

// NeedsElevation implements [Manager].
func (Winget) NeedsElevation() bool { return false }

// UpgradeCmd implements [Manager]. -e makes --id an exact match, so
// upgrading "Git.Git" can never pick up "Git.Git.Preview"; --silent and
// --disable-interactivity keep the package's own installer from showing a
// wizard or prompt Devpit can't answer; and both --accept-*-agreements
// flags stop winget itself from waiting on a "Do you agree? [Y/N]" nobody
// will see.
func (Winget) UpgradeCmd(id string) []string {
	return []string{
		"winget", "upgrade", "--id", id, "-e", "--silent",
		"--accept-package-agreements", "--accept-source-agreements",
		"--disable-interactivity",
	}
}

// CheckCmds implements [Manager]. One step: `winget upgrade` with no
// package lists what has an update, refreshing its sources as it goes.
func (w Winget) CheckCmds() [][]string { return [][]string{w.OutdatedCmd()} }

// CleanupCmds implements [Manager]. winget removes an old version as part
// of upgrading it and keeps no download cache worth clearing.
func (Winget) CleanupCmds([]string) [][]string { return nil }

// noPackagesPhrases are the messages winget prints instead of a table when
// there is nothing to report, across the list and upgrade commands. They
// are English: a localized "nothing to update" is not recognised, which
// leaves [OutdatedReport.Parsed] false and the caller falling back to
// upgrading everything, the safe direction to be wrong in.
var noPackagesPhrases = []string{
	"no installed package found",
	"no applicable update found",
	"no applicable upgrade found",
	"no available upgrade",
}

// wingetUnknownRE and wingetPinnedRE match winget's footer counts of
// packages it left out of the upgrade table. English only: the counts are
// a nice-to-have, while the table itself is read without depending on the
// display language (see [parseWingetTables]). Newer winget words the
// pinned footer "have pins that prevent upgrade", older builds "are
// pinned and need to be explicitly upgraded"; both are accepted.
var (
	wingetUnknownRE = regexp.MustCompile(`(?i)^(\d+)\s+package\(s\)\s+have version numbers that cannot be determined`)
	wingetPinnedRE  = regexp.MustCompile(`(?i)^(\d+)\s+package\(s\)\s+(?:are pinned|have pins)`)
)

// hasNoPackagesPhrase reports whether out contains one of
// [noPackagesPhrases].
func hasNoPackagesPhrase(out string) bool {
	lower := strings.ToLower(out)
	for _, phrase := range noPackagesPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// ParseList implements [Manager]. winget's output is a fixed-width table:
//
//	Name                 Id                      Version      Source
//	-----------------------------------------------------------------
//	7-Zip                7zip.7zip               23.01        winget
//	Git                  Git.Git                 2.43.0       winget
//
// The table is found and sliced by [parseWingetTables], which goes by the
// dashed separator and column positions rather than the header's words, so
// it reads the same on a German or Japanese Windows. The first three
// columns are always Name, Id and Version; any Available and Source columns
// after them are ignored.
func (Winget) ParseList(out string) []Installed {
	tables := parseWingetTables(out)
	if len(tables) == 0 {
		return nil
	}
	var result []Installed
	for _, t := range tables {
		if t.columns < 3 {
			continue
		}
		for _, r := range t.rows {
			result = append(result, Installed{Name: r[0], ID: r[1], Version: r[2]})
		}
	}
	return result
}

// ParseOutdated implements [Manager]. It is [Winget.ParseOutdatedReport]'s
// Packages.
func (w Winget) ParseOutdated(out string) []Outdated {
	return w.ParseOutdatedReport(out).Packages
}

// ParseOutdatedReport implements [Manager]. winget upgrade's table adds an
// "Available" column holding the version an upgrade would install, and may
// be followed by a second table of packages that `--all` skips:
//
//	Name            Id              Version      Available    Source
//	------------------------------------------------------------------
//	Git             Git.Git         2.42.0       2.43.0       winget
//	1 upgrades available.
//
//	The following packages have an upgrade available, but require explicit targeting for upgrade:
//	Name            Id              Version      Available    Source
//	------------------------------------------------------------------
//	Node.js         OpenJS.NodeJS   20.10.0      22.1.0       winget
//	1 package(s) have version numbers that cannot be determined. Use --include-unknown to see all results.
//
// Columns are positional (Name, Id, Version, Available, then an optional
// Source), never looked up by their header text, so the parser works under
// any display language. Every table after the first is the
// explicit-targeting one, and so is a table introduced by a sentence ending
// in a colon (which is how winget prints it when it is the only table); its
// rows are marked [Outdated.Explicit].
func (Winget) ParseOutdatedReport(out string) OutdatedReport {
	var rep OutdatedReport
	for i, t := range parseWingetTables(out) {
		if t.columns < 4 {
			// Not an upgrade table: nothing with fewer columns than
			// Name, Id, Version, Available can say what to upgrade to.
			continue
		}
		rep.Parsed = true
		for _, r := range t.rows {
			rep.Packages = append(rep.Packages, Outdated{
				Name:     r[0],
				ID:       r[1],
				Current:  r[2],
				Latest:   r[3],
				Explicit: i > 0 || t.introduced,
			})
		}
	}

	for _, line := range normalizeWingetLines(out) {
		line = strings.TrimSpace(line)
		if m := wingetUnknownRE.FindStringSubmatch(line); m != nil {
			rep.Unknown, _ = strconv.Atoi(m[1])
			rep.Parsed = true
		}
		if m := wingetPinnedRE.FindStringSubmatch(line); m != nil {
			rep.Pinned, _ = strconv.Atoi(m[1])
			rep.Parsed = true
		}
	}

	if hasNoPackagesPhrase(out) {
		rep.Parsed = true
	}
	return rep
}
