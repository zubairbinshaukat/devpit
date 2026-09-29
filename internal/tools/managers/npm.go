package managers

import (
	"encoding/json"
	"strings"
)

// NPM drives npm's global package installs (node_modules alongside the
// Node.js install itself, not a project's local dependencies). It is never
// picked as the machine's [Preferred] manager; it always runs as its own
// Update step, since global npm packages exist independently of whichever
// of Scoop, winget or Chocolatey installed Node itself.
type NPM struct{}

// Name implements [Manager].
func (NPM) Name() string { return "npm" }

// ListCmd implements [Manager].
func (NPM) ListCmd() []string { return []string{"npm", "ls", "-g", "--json", "--depth=0"} }

// OutdatedCmd implements [Manager].
func (NPM) OutdatedCmd() []string { return []string{"npm", "outdated", "-g", "--json"} }

// UpgradeAllCmds implements [Manager].
func (NPM) UpgradeAllCmds() [][]string { return [][]string{{"npm", "update", "-g"}} }

// InstallCmd implements [Manager].
func (NPM) InstallCmd(id string) []string { return []string{"npm", "install", "-g", id} }

// UpgradeCmd implements [Manager]. It installs pkg@latest rather than
// running `npm update -g pkg`: update resolves against the package's
// "wanted" range, and for a global package whose installed version is newer
// than the registry's latest tag (a prerelease someone installed on
// purpose) it can quietly downgrade it. @latest says exactly what is meant.
func (NPM) UpgradeCmd(id string) []string {
	return []string{"npm", "install", "-g", id + "@latest"}
}

// CheckCmds implements [Manager].
func (n NPM) CheckCmds() [][]string { return [][]string{n.OutdatedCmd()} }

// CleanupCmds implements [Manager]. npm replaces a global package in
// place; its cache is shared with every project and not Devpit's to prune.
func (NPM) CleanupCmds([]string) [][]string { return nil }

// NeedsElevation implements [Manager]. npm's global prefix lives under the
// current user's Node.js install on Windows, so it never needs elevation.
func (NPM) NeedsElevation() bool { return false }

// npmListOutput mirrors the shape of "npm ls -g --json --depth=0":
//
//	{
//	  "dependencies": {
//	    "npm":  {"version": "10.2.4"},
//	    "pnpm": {"version": "8.15.1"}
//	  }
//	}
type npmListOutput struct {
	Dependencies map[string]struct {
		Version string `json:"version"`
	} `json:"dependencies"`
}

// ParseList implements [Manager]. Invalid or empty JSON parses to no
// packages rather than an error, matching every other manager's lenient
// parsing here.
func (NPM) ParseList(out string) []Installed {
	var parsed npmListOutput
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		return nil
	}
	if len(parsed.Dependencies) == 0 {
		return nil
	}
	result := make([]Installed, 0, len(parsed.Dependencies))
	for name, dep := range parsed.Dependencies {
		result = append(result, Installed{Name: name, ID: name, Version: dep.Version})
	}
	sortInstalled(result)
	return result
}

// npmOutdatedEntry mirrors one value of "npm outdated -g --json"'s object,
// keyed by package name:
//
//	{
//	  "npm": {"current": "10.2.0", "wanted": "10.2.4", "latest": "10.2.4"}
//	}
//
// When the same package is outdated in more than one place npm writes an
// array of these for that name instead of a single object; see
// [decodeNPMOutdatedEntry].
type npmOutdatedEntry struct {
	Current string `json:"current"`
	Latest  string `json:"latest"`
}

// ParseOutdated implements [Manager]. It is [NPM.ParseOutdatedReport]'s
// Packages.
func (n NPM) ParseOutdated(out string) []Outdated {
	return n.ParseOutdatedReport(out).Packages
}

// ParseOutdatedReport implements [Manager]. npm exits 1 whenever anything
// is outdated, so the caller must parse the output of a "failed" check
// step too; the exit code says nothing about whether the JSON is good.
//
// When everything is current, npm prints nothing at all (empty stdout,
// exit code 0) rather than "{}"; both parse to a Parsed report with no
// packages. Output that is not a JSON object, or is npm's own
// {"error": {...}} object, is not Parsed. An entry with no latest version,
// or whose latest is what is already installed, has nothing to upgrade to
// and is left out.
func (NPM) ParseOutdatedReport(out string) OutdatedReport {
	if strings.TrimSpace(out) == "" {
		return OutdatedReport{Parsed: true}
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		return OutdatedReport{}
	}
	if raw, ok := parsed["error"]; ok && isNPMError(raw) {
		return OutdatedReport{}
	}
	rep := OutdatedReport{Parsed: true}
	for name, raw := range parsed {
		entry, ok := decodeNPMOutdatedEntry(raw)
		if !ok || entry.Latest == "" || entry.Latest == entry.Current {
			continue
		}
		rep.Packages = append(rep.Packages, Outdated{
			Name:    name,
			ID:      name,
			Current: entry.Current,
			Latest:  entry.Latest,
		})
	}
	sortOutdated(rep.Packages)
	return rep
}

// decodeNPMOutdatedEntry decodes one value of npm outdated's object, which
// is a single entry or, when the package is outdated in several places, an
// array of them; the first is taken, since a global install only has one
// place that matters.
func decodeNPMOutdatedEntry(raw json.RawMessage) (npmOutdatedEntry, bool) {
	var entry npmOutdatedEntry
	if err := json.Unmarshal(raw, &entry); err == nil {
		return entry, true
	}
	var list []npmOutdatedEntry
	if err := json.Unmarshal(raw, &list); err == nil && len(list) > 0 {
		return list[0], true
	}
	return npmOutdatedEntry{}, false
}

// isNPMError reports whether raw, the value of a top-level "error" key, is
// npm's own failure report ({"code": "E404", "summary": "..."}) rather than
// an outdated package that happens to be called "error".
func isNPMError(raw json.RawMessage) bool {
	var e struct {
		Code    string `json:"code"`
		Summary string `json:"summary"`
		Latest  string `json:"latest"`
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		return false
	}
	return (e.Code != "" || e.Summary != "") && e.Latest == ""
}
