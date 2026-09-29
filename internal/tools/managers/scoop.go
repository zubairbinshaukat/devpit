package managers

import "strings"

// Scoop drives the Scoop package manager (https://scoop.sh). It runs
// entirely as the current user; Scoop installs never need elevation.
type Scoop struct{}

// Name implements [Manager].
func (Scoop) Name() string { return "scoop" }

// ListCmd implements [Manager].
func (Scoop) ListCmd() []string { return []string{"scoop", "list"} }

// OutdatedCmd implements [Manager].
func (Scoop) OutdatedCmd() []string { return []string{"scoop", "status"} }

// UpgradeAllCmds implements [Manager]. Scoop needs four steps: refresh the
// bucket manifests, update every app, then clean up old versions and the
// download cache so an "update everything" run doesn't leave gigabytes of
// stale app versions behind.
func (Scoop) UpgradeAllCmds() [][]string {
	return [][]string{
		{"scoop", "update"},
		{"scoop", "update", "*"},
		{"scoop", "cleanup", "*"},
		{"scoop", "cache", "rm", "-a"},
	}
}

// InstallCmd implements [Manager].
func (Scoop) InstallCmd(id string) []string { return []string{"scoop", "install", id} }

// UpgradeCmd implements [Manager]. It assumes the bucket manifests were
// already refreshed by the check ([Scoop.CheckCmds]), so it does not repeat
// the bare `scoop update` for every app.
func (Scoop) UpgradeCmd(id string) []string { return []string{"scoop", "update", id} }

// CheckCmds implements [Manager]. `scoop status` compares installed apps
// against the local copy of each bucket's manifests, so a bare `scoop
// update` has to pull the buckets first or the check would miss every
// release since the last refresh.
func (s Scoop) CheckCmds() [][]string {
	return [][]string{{"scoop", "update"}, s.OutdatedCmd()}
}

// CleanupCmds implements [Manager]. After upgrading ids, `scoop cleanup`
// removes the versions they no longer use and `scoop cache rm` their
// downloaded installers, the same housekeeping [Scoop.UpgradeAllCmds] does
// for every app with "*" and "-a", limited to what this run touched.
func (Scoop) CleanupCmds(ids []string) [][]string {
	if len(ids) == 0 {
		return nil
	}
	cleanup := append([]string{"scoop", "cleanup"}, ids...)
	cache := append([]string{"scoop", "cache", "rm"}, ids...)
	return [][]string{cleanup, cache}
}

// NeedsElevation implements [Manager].
func (Scoop) NeedsElevation() bool { return false }

// ParseList implements [Manager]. It parses "scoop list" output, a table
// whose header is "Name ... Version ... Source ... Updated ... Info"
// followed by a dashed separator line, for example:
//
//	Installed apps:
//
//	Name         Version      Source  Updated             Info
//	----         -------      ------  -------             ----
//	git          2.43.0       main    2024-01-15 10:23:11
//	nodejs-lts   20.11.0      main    2024-01-10 09:00:00
func (Scoop) ParseList(out string) []Installed {
	var result []Installed
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if !isScoopTableRow(fields) {
			continue
		}
		result = append(result, Installed{
			Name:    fields[0],
			ID:      fields[0],
			Version: fields[1],
		})
	}
	return result
}

// ParseOutdated implements [Manager]. It is [Scoop.ParseOutdatedReport]'s
// Packages.
func (s Scoop) ParseOutdated(out string) []Outdated {
	return s.ParseOutdatedReport(out).Packages
}

// ParseOutdatedReport implements [Manager]. It parses "scoop status"
// output, whose table lists only apps with an update available:
//
//	Scoop is up to date.
//
//	Name        Installed Version  Latest Version  Missing Dependencies  Info
//	----        -----------------  --------------  ---------------------  ----
//	git         2.42.0             2.43.0
//	nodejs-lts  20.10.0            20.11.0                                Held package
//
// An app whose Info says "Held package" is marked [Outdated.Held]. When
// everything is current, Scoop prints only "Scoop is up to date." (and,
// on some versions, "Everything is ok!") with no table; that is still a
// recognised answer, so the report is Parsed with no packages.
//
// A row whose third column is not a version (an app whose manifest was
// removed from its bucket, say, where Scoop shows only an Info note) has
// nothing to upgrade to and is left out.
func (Scoop) ParseOutdatedReport(out string) OutdatedReport {
	var rep OutdatedReport
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && strings.HasPrefix(fields[0], "----") {
			rep.Parsed = true
		}
		if !isScoopTableRow(fields) {
			continue
		}
		if len(fields) < 3 || !strings.ContainsAny(fields[2], "0123456789") {
			continue
		}
		rep.Packages = append(rep.Packages, Outdated{
			Name:    fields[0],
			ID:      fields[0],
			Current: fields[1],
			Latest:  fields[2],
			Held:    strings.Contains(line, "Held package"),
		})
	}
	lower := strings.ToLower(out)
	if strings.Contains(lower, "is up to date") || strings.Contains(lower, "everything is ok") {
		rep.Parsed = true
	}
	return rep
}

// isScoopTableRow reports whether fields looks like a data row of a Scoop
// table rather than a blank line, a status message, a header or the dashed
// separator beneath it.
func isScoopTableRow(fields []string) bool {
	if len(fields) < 2 {
		return false
	}
	switch fields[0] {
	case "Name", "Scoop", "Installed", "----":
		return false
	}
	if strings.HasPrefix(fields[0], "----") {
		return false
	}
	// A real row's second field is always a version, which always has a
	// digit; this rejects stray prose lines like "Everything is ok!".
	return strings.ContainsAny(fields[1], "0123456789")
}
