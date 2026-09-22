package managers

import "strings"

// Choco drives Chocolatey. Every Chocolatey command needs an elevated
// shell, so Devpit always routes it through the elevated worker.
type Choco struct{}

// Name implements [Manager].
func (Choco) Name() string { return "choco" }

// ListCmd implements [Manager].
func (Choco) ListCmd() []string { return []string{"choco", "list"} }

// OutdatedCmd implements [Manager].
func (Choco) OutdatedCmd() []string { return []string{"choco", "outdated"} }

// UpgradeAllCmds implements [Manager].
func (Choco) UpgradeAllCmds() [][]string {
	return [][]string{{"choco", "upgrade", "all", "-y"}}
}

// InstallCmd implements [Manager].
func (Choco) InstallCmd(id string) []string { return []string{"choco", "install", id, "-y"} }

// NeedsElevation implements [Manager]. Chocolatey installs to
// %ProgramData%\chocolatey and needs an admin shell for every command.
func (Choco) NeedsElevation() bool { return true }

// ParseList implements [Manager]. "choco list" output is one
// "name version" pair per line, bracketed by a banner and a trailing
// count:
//
//	Chocolatey v2.2.2
//	git 2.43.0
//	nodejs-lts 20.11.0
//	2 packages installed.
func (Choco) ParseList(out string) []Installed {
	var result []Installed
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || !isChocoPackageLine(line) {
			continue
		}
		result = append(result, Installed{Name: fields[0], ID: fields[0], Version: fields[1]})
	}
	return result
}

// ParseOutdated implements [Manager]. "choco outdated" prints a
// pipe-delimited table:
//
//	Chocolatey v2.2.2
//	Outdated Packages
//	 Output is package name | current version | available version | pinned?
//
//	git|2.42.0|2.43.0|false
//	nodejs-lts|20.10.0|20.11.0|false
//
//	Chocolatey has determined 2 package(s) are outdated.
func (Choco) ParseOutdated(out string) []Outdated {
	var result []Outdated
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "|") || strings.HasPrefix(line, "Output is") {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 3 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		if name == "" {
			continue
		}
		result = append(result, Outdated{
			Name:    name,
			ID:      name,
			Current: strings.TrimSpace(parts[1]),
			Latest:  strings.TrimSpace(parts[2]),
		})
	}
	return result
}

// isChocoPackageLine reports whether a two-field line looks like a
// "name version" package row rather than the trailing count line, for
// example "2 packages installed." (which also splits into two fields:
// "2" and "packages").
func isChocoPackageLine(line string) bool {
	if strings.HasPrefix(line, "Chocolatey") {
		return false
	}
	if strings.Contains(line, "packages installed") || strings.Contains(line, "package installed") {
		return false
	}
	return true
}
